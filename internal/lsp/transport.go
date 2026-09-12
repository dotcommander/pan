package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// maxFrameBytes bounds one decoded server frame so a misbehaving server
// cannot make pan allocate without limit.
const maxFrameBytes = 32 << 20

// jsonRPCVersion is the protocol version every LSP message carries.
const jsonRPCVersion = "2.0"

// jsonNull is the JSON null literal, the LSP "no result" answer.
const jsonNull = "null"

// rpcRequest is one client-to-server JSON-RPC message. A zero ID marks a
// notification; pan never sends id 0 as a request because IDs start at 1.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcResponse is one server-to-client JSON-RPC message. ID is nil for
// notifications; Method is set when the message is a server request that
// expects a reply.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is one JSON-RPC error object from the server.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("lsp: server error %d: %s", e.Code, e.Message)
}

// rpcResult carries one answered request back to its caller.
type rpcResult struct {
	Data json.RawMessage
	Err  error
}

// call sends one request and decodes its result into result when non-nil.
func (c *Client) call(ctx context.Context, method string, params, result any) error {
	raw, err := c.callRaw(ctx, method, params)
	if err != nil {
		return err
	}
	if result != nil && len(raw) > 0 && string(raw) != jsonNull {
		if err := json.Unmarshal(raw, result); err != nil {
			return fmt.Errorf("lsp: decode %s result: %w", method, err)
		}
	}
	return nil
}

// callRaw sends one request and returns its undecoded result.
func (c *Client) callRaw(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan rpcResult, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.send(rpcRequest{JSONRPC: jsonRPCVersion, ID: id, Method: method, Params: params}); err != nil {
		return nil, fmt.Errorf("lsp: send %s: %w", method, err)
	}
	select {
	case result := <-ch:
		return result.Data, result.Err
	case <-c.done:
		return nil, ErrServerDied
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// notify sends one notification and does not wait for a reply.
func (c *Client) notify(ctx context.Context, method string, params any) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return c.send(rpcRequest{JSONRPC: jsonRPCVersion, Method: method, Params: params})
}

// send frames one message with Content-Length headers and writes it.
func (c *Client) send(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, writeErr := io.WriteString(c.writer, header); writeErr != nil {
		return writeErr
	}
	_, err = c.writer.Write(data)
	return err
}

// readAnswer reads one Content-Length framed server message.
func readAnswer(reader io.Reader) ([]byte, error) {
	var contentLength int
	for {
		line, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		name, value, found := strings.Cut(line, ":")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		length, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || length < 0 {
			return nil, fmt.Errorf("lsp: invalid Content-Length %q", line)
		}
		contentLength = length
	}
	if contentLength == 0 {
		return nil, nil
	}
	if contentLength > maxFrameBytes {
		return nil, fmt.Errorf("lsp: frame of %d bytes exceeds the %d byte cap", contentLength, maxFrameBytes)
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

// readLine reads one \n-terminated header line, tolerating \r\n.
func readLine(reader io.Reader) (string, error) {
	var builder strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return builder.String(), nil
			}
			builder.WriteByte(buf[0])
		}
		if err != nil {
			return builder.String(), err
		}
	}
}

// readLoop routes server messages until the transport fails or closes.
// Notifications are dropped; server requests are answered with a null
// result (pan implements no server-facing handlers); responses are routed
// to their pending caller. It closes done exactly once on exit.
func (c *Client) readLoop() {
	defer close(c.done)
	for {
		body, err := readAnswer(c.reader)
		if err != nil {
			return
		}
		if len(body) == 0 {
			continue
		}
		var resp rpcResponse
		if json.Unmarshal(body, &resp) != nil {
			continue
		}
		if resp.ID == nil {
			continue
		}
		if resp.Method != "" {
			_ = c.send(struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      int64           `json:"id"`
				Result  json.RawMessage `json:"result"`
			}{JSONRPC: jsonRPCVersion, ID: *resp.ID, Result: json.RawMessage(jsonNull)})
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[*resp.ID]
		c.mu.Unlock()
		if !ok {
			continue
		}
		if resp.Error != nil {
			ch <- rpcResult{Err: resp.Error}
			continue
		}
		ch <- rpcResult{Data: resp.Result}
	}
}
