package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// ErrServerDied reports a language server that exited, or a transport that
// failed, before a request was answered.
var ErrServerDied = errors.New("language server died")

// shutdownWait bounds how long Shutdown waits for a server process to exit
// after the transport closes before killing it.
const shutdownWait = 2 * time.Second

// Client is one synchronous language-server connection speaking the LSP
// base protocol (JSON-RPC 2.0 with Content-Length framing) over one duplex
// stream. Queries are sequential; responses are matched by request id. The
// zero value is not usable: construct through Start or OverIO.
type Client struct {
	reader *bufio.Reader
	writer io.Writer
	closer io.Closer
	waiter func(ctx context.Context) error // waits for process exit; nil for in-process transports

	nextID  atomic.Int64
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan rpcResult
	done    chan struct{} // closed when readLoop exits

	initDone atomic.Bool

	shutdownOnce sync.Once
	shutdownErr  error
}

// OverIO connects one client to an in-process duplex transport. It exists
// for tests and alternative transports; production code uses Start.
func OverIO(conn io.ReadWriteCloser) *Client {
	return newClient(bufio.NewReaderSize(conn, 64*1024), conn, conn, nil)
}

// Start launches one local language-server subprocess and connects a
// client to its stdio. Server stderr is discarded; the subprocess is
// bounded by ctx. The command must already exist on PATH — callers resolve
// availability first (see Service). The executable is resolved with an
// explicit LookPath so the launched path is pinned rather than re-resolved
// through PATH at exec time.
func Start(ctx context.Context, command string, args ...string) (*Client, error) {
	resolved, err := exec.LookPath(command)
	if err != nil {
		return nil, fmt.Errorf("lsp: resolve server command %s: %w", command, err)
	}
	cmd := &exec.Cmd{
		Path:   resolved,
		Args:   append([]string{command}, args...),
		Stderr: io.Discard,
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("lsp: start %s: %w", command, err)
	}
	// The watcher goroutine exits as soon as ctx is done; until then it
	// parks on ctx.Done, bounding the server's lifetime by the query
	// context exactly as a context-bound exec command would.
	go func() {
		<-ctx.Done()
		_ = cmd.Process.Kill()
	}()
	return newClient(bufio.NewReaderSize(stdout, 64*1024), stdin, stdin, func(ctx context.Context) error {
		return waitProcess(ctx, cmd)
	}), nil
}

func newClient(reader *bufio.Reader, writer io.Writer, closer io.Closer, waiter func(context.Context) error) *Client {
	c := &Client{
		reader:  reader,
		writer:  writer,
		closer:  closer,
		waiter:  waiter,
		pending: make(map[int64]chan rpcResult),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// waitProcess waits for the server to exit after stdin closes, killing it
// when the context or the shutdown window expires first.
func waitProcess(ctx context.Context, cmd *exec.Cmd) error {
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	timer := time.NewTimer(shutdownWait)
	defer timer.Stop()
	select {
	case err := <-waitCh:
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	case <-ctx.Done():
		return killAndWait(cmd, waitCh, ctx.Err())
	case <-timer.C:
		return killAndWait(cmd, waitCh, fmt.Errorf("lsp: server did not exit within %s", shutdownWait))
	}
}

func killAndWait(cmd *exec.Cmd, waitCh <-chan error, cause error) error {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	<-waitCh
	return cause
}

// Initialize performs the initialize/initialized handshake for one
// workspace root. It must complete before queries are sent.
func (c *Client) Initialize(ctx context.Context, rootPath string) error {
	var result json.RawMessage
	if err := c.call(ctx, "initialize", InitializeParams{
		RootURI:      PathToURI(rootPath),
		Capabilities: panCapabilities(),
	}, &result); err != nil {
		return fmt.Errorf("lsp: initialize: %w", err)
	}
	if err := c.notify(ctx, "initialized", struct{}{}); err != nil {
		return fmt.Errorf("lsp: initialized: %w", err)
	}
	c.initDone.Store(true)
	return nil
}

// Definition resolves the symbol at one position to its definition
// locations. It accepts every defined response shape (Location,
// []Location, LocationLink[], or null) and normalizes to locations; a
// null or empty answer yields nil, not an error.
func (c *Client) Definition(ctx context.Context, file string, line, column int) ([]Location, error) {
	raw, err := c.callRaw(ctx, "textDocument/definition", positionParams(file, line, column))
	if err != nil {
		return nil, fmt.Errorf("lsp: definition: %w", err)
	}
	return decodeLocations(raw)
}

// References resolves the references to the symbol at one position.
func (c *Client) References(ctx context.Context, file string, line, column int) ([]Location, error) {
	raw, err := c.callRaw(ctx, "textDocument/references", ReferenceParams{
		TextDocumentPositionParams: positionParams(file, line, column),
		Context:                    ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		return nil, fmt.Errorf("lsp: references: %w", err)
	}
	return decodeLocations(raw)
}

// Hover resolves the hover payload at one position; nil means the server
// answered null (nothing at that position).
func (c *Client) Hover(ctx context.Context, file string, line, column int) (*HoverResult, error) {
	raw, err := c.callRaw(ctx, "textDocument/hover", positionParams(file, line, column))
	if err != nil {
		return nil, fmt.Errorf("lsp: hover: %w", err)
	}
	if len(raw) == 0 || string(raw) == jsonNull {
		return nil, nil
	}
	var hover HoverResult
	if err := json.Unmarshal(raw, &hover); err != nil {
		return nil, fmt.Errorf("lsp: decode hover: %w", err)
	}
	return &hover, nil
}

// DocumentSymbols resolves the symbols of one document, accepting both the
// hierarchical DocumentSymbol and flat SymbolInformation shapes.
func (c *Client) DocumentSymbols(ctx context.Context, file string) ([]DocumentSymbol, error) {
	raw, err := c.callRaw(ctx, "textDocument/documentSymbol", DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: PathToURI(file)},
	})
	if err != nil {
		return nil, fmt.Errorf("lsp: documentSymbol: %w", err)
	}
	if len(raw) == 0 || string(raw) == jsonNull {
		return nil, nil
	}
	if isSymbolInformationArray(raw) {
		var infos []SymbolInformation
		if err := json.Unmarshal(raw, &infos); err != nil {
			return nil, fmt.Errorf("lsp: decode symbol information: %w", err)
		}
		symbols := make([]DocumentSymbol, 0, len(infos))
		for _, info := range infos {
			symbols = append(symbols, DocumentSymbol{
				Name:           info.Name,
				Kind:           info.Kind,
				Range:          info.Location.Range,
				SelectionRange: info.Location.Range,
			})
		}
		return symbols, nil
	}
	var symbols []DocumentSymbol
	if err := json.Unmarshal(raw, &symbols); err != nil {
		return nil, fmt.Errorf("lsp: decode document symbols: %w", err)
	}
	return symbols, nil
}

// DidOpen publishes one document's current content so the server can
// answer queries about it without filesystem access.
func (c *Client) DidOpen(ctx context.Context, file, languageID, content string) error {
	return c.notify(ctx, "textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        PathToURI(file),
			LanguageID: languageID,
			Version:    1,
			Text:       content,
		},
	})
}

// Shutdown ends the session: shutdown request, exit notification, then
// transport close and process wait. It is idempotent and always closes the
// transport, so it is safe in defer paths.
func (c *Client) Shutdown(ctx context.Context) error {
	c.shutdownOnce.Do(func() { c.shutdownErr = c.closeSession(ctx) })
	return c.shutdownErr
}

// closeSession performs the shutdown/exit/close/wait sequence once.
func (c *Client) closeSession(ctx context.Context) error {
	var firstErr error
	if c.initDone.Load() {
		qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownWait)
		defer cancel()
		if err := c.call(qctx, "shutdown", nil, nil); err != nil && !errors.Is(err, ErrServerDied) && firstErr == nil {
			firstErr = err
		}
		if err := c.notify(qctx, "exit", nil); err != nil && !errors.Is(err, ErrServerDied) && firstErr == nil {
			firstErr = err
		}
	}
	if err := c.closer.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if c.waiter != nil {
		if err := c.waiter(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// positionParams builds the base position request for one file.
func positionParams(file string, line, column int) TextDocumentPositionParams {
	return TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: PathToURI(file)},
		Position:     Position{Line: line, Character: column},
	}
}

// decodeLocations normalizes every defined location response shape.
func decodeLocations(raw json.RawMessage) ([]Location, error) {
	if len(raw) == 0 || string(raw) == jsonNull {
		return nil, nil
	}
	if raw[0] == '[' {
		// Each candidate shape is probed in turn: a decode that does not
		// fit means "different shape, try the next", not a failure.
		var locations []Location
		if json.Unmarshal(raw, &locations) == nil && locationsArePopulated(locations) {
			return locations, nil
		}
		var links []LocationLink
		if json.Unmarshal(raw, &links) == nil && len(links) > 0 && links[0].TargetURI != "" {
			out := make([]Location, 0, len(links))
			for _, link := range links {
				out = append(out, Location{URI: link.TargetURI, Range: link.TargetRange})
			}
			return out, nil
		}
		return nil, nil
	}
	var single Location
	decoded := json.Unmarshal(raw, &single) == nil
	if decoded && single.URI != "" {
		return []Location{single}, nil
	}
	// A non-array answer that is not one populated Location is one of
	// the "no definition" response shapes: treat it as empty.
	return nil, nil
}

// locationsArePopulated distinguishes a decoded Location array from an
// empty or misfit decode.
func locationsArePopulated(locations []Location) bool {
	return len(locations) > 0 && locations[0].URI != ""
}

// panCapabilities declares the client capability subset pan relies on.
func panCapabilities() ClientCapabilities {
	var caps ClientCapabilities
	caps.TextDocument.Hover.DynamicRegistration = false
	caps.TextDocument.Definition.DynamicRegistration = false
	caps.TextDocument.References.DynamicRegistration = false
	caps.TextDocument.DocumentSymbol.DynamicRegistration = false
	caps.TextDocument.DocumentSymbol.HierarchicalDocumentSymbolSupport = true
	return caps
}
