package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

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

	if err := c.send(ctx, rpcRequest{JSONRPC: jsonRPCVersion, ID: id, Method: method, Params: params}); err != nil {
		return nil, fmt.Errorf("lsp: send %s: %w", method, err)
	}
	select {
	case result := <-ch:
		return result.Data, result.Err
	case <-c.done:
		return nil, ErrServerDied
	case <-ctx.Done():
		c.closeTransport()
		return nil, ctx.Err()
	}
}

// notify sends one notification and does not wait for a reply.
func (c *Client) notify(ctx context.Context, method string, params any) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return c.send(ctx, rpcRequest{JSONRPC: jsonRPCVersion, Method: method, Params: params})
}

// send frames one message with Content-Length headers and writes it.
func (c *Client) send(ctx context.Context, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrServerDied
	case <-c.writeGate:
	}
	defer func() { c.writeGate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}
	finished := make(chan error, 1)
	go func() {
		// Closing the owned transport interrupts this worker's writes.
		frame := append([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))), data...)
		n, err := c.writer.Write(frame)
		if err == nil && n != len(frame) {
			err = io.ErrShortWrite
		}
		finished <- err
	}()
	select {
	case err := <-finished:
		if err != nil {
			c.closeTransport()
		}
		return err
	case <-ctx.Done():
		c.closeTransport()
		<-finished
		return ctx.Err()
	case <-c.done:
		c.closeTransport()
		<-finished
		return ErrServerDied
	}
}

// readAnswer preserves the default-policy test and alternative-transport API.
func readAnswer(reader io.Reader) ([]byte, error) {
	return readAnswerWithOptions(reader, TransportOptions{}.normalized())
}

func readAnswerWithOptions(reader io.Reader, options TransportOptions) ([]byte, error) {
	contentLength := -1
	total, headers := 0, 0
	for {
		line, size, err := readHeaderLine(reader, min(options.MaxHeaderLineBytes, options.MaxHeaderBytes-total))
		if err != nil {
			return nil, err
		}
		total += size
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		headers++
		if headers > options.MaxHeaders {
			return nil, fmt.Errorf("lsp: header count exceeds cap")
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			return nil, fmt.Errorf("lsp: malformed header")
		}
		if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		if contentLength >= 0 {
			return nil, fmt.Errorf("lsp: duplicate Content-Length")
		}
		length, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || length < 0 {
			return nil, fmt.Errorf("lsp: invalid Content-Length")
		}
		contentLength = length
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("lsp: missing Content-Length")
	}
	if contentLength > options.MaxFrameBytes {
		return nil, fmt.Errorf("lsp: frame exceeds byte cap")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

func readLine(reader io.Reader) (string, error) {
	line, _, err := readHeaderLine(reader, TransportOptions{}.normalized().MaxHeaderLineBytes)
	return line, err
}

// The cap includes CR/LF delimiters and is checked before every append.
func readHeaderLine(reader io.Reader, limit int) (string, int, error) {
	var builder strings.Builder
	var buf [1]byte
	for size := 0; ; {
		if size >= limit {
			return "", size, fmt.Errorf("lsp: headers exceed byte cap")
		}
		n, err := reader.Read(buf[:])
		if n > 0 {
			size++
			if buf[0] == '\n' {
				return builder.String(), size, nil
			}
			builder.WriteByte(buf[0])
		}
		if err != nil {
			return "", size, err
		}
	}
}

// readLoop routes server messages until the transport fails or closes.
// Notifications are dropped; server requests are answered with a null
// result (pan implements no server-facing handlers); responses are routed
// to their pending caller. It closes done exactly once on exit.
func (c *Client) readLoop() {
	defer close(c.done)
	defer c.closeTransport()
	for {
		body, err := readAnswerWithOptions(c.reader, c.options)
		if err != nil {
			return
		}
		if len(body) == 0 {
			continue
		}
		var resp rpcResponse
		if json.Unmarshal(body, &resp) != nil || resp.JSONRPC != jsonRPCVersion {
			return
		}
		if resp.ID == nil {
			continue
		}
		if resp.Method != "" {
			ctx, cancel := context.WithTimeout(c.ctx, shutdownWait)
			err := c.send(ctx, struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      int64           `json:"id"`
				Result  json.RawMessage `json:"result"`
			}{JSONRPC: jsonRPCVersion, ID: *resp.ID, Result: json.RawMessage(jsonNull)})
			cancel()
			if err != nil {
				return
			}
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[*resp.ID]
		c.mu.Unlock()
		if !ok {
			continue
		}
		if resp.Error != nil {
			select {
			case ch <- rpcResult{Err: resp.Error}:
			default:
			}
			continue
		}
		select {
		case ch <- rpcResult{Data: resp.Result}:
		default:
		}
	}
}
