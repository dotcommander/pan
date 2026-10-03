package lsp

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReadAnswerBoundsHeadersAndFrames(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, frame string
		options     TransportOptions
	}{
		{"line", strings.Repeat("a", 9), TransportOptions{MaxHeaderLineBytes: 8}},
		{"aggregate", "X: a\r\nY: b\r\n\r\n", TransportOptions{MaxHeaderBytes: 12}},
		{"count", "X: a\r\nY: b\r\nContent-Length: 0\r\n\r\n", TransportOptions{MaxHeaders: 2}},
		{"body", "Content-Length: 9\r\n\r\n", TransportOptions{MaxFrameBytes: 8}},
		{"duplicate", "Content-Length: 0\r\nContent-Length: 0\r\n\r\n", TransportOptions{}},
		{"missing", "X: a\r\n\r\n", TransportOptions{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := readAnswerWithOptions(strings.NewReader(tt.frame), tt.options.normalized()); err == nil {
				t.Fatal("accepted invalid framing")
			}
		})
	}
	data, err := readAnswerWithOptions(strings.NewReader("Content-Length: 2\r\n\r\n{}"), TransportOptions{MaxFrameBytes: 2, MaxHeaderLineBytes: 19, MaxHeaderBytes: 21}.normalized())
	if err != nil || string(data) != "{}" {
		t.Fatalf("boundary frame = %q, %v", data, err)
	}
}

type blockedTransport struct {
	entered chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (b *blockedTransport) Read([]byte) (int, error) { <-b.closed; return 0, io.EOF }
func (b *blockedTransport) Write([]byte) (int, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.closed
	return 0, io.ErrClosedPipe
}
func (b *blockedTransport) Close() error { b.once.Do(func() { close(b.closed) }); return nil }

func TestCancellationJoinsBlockedSend(t *testing.T) {
	t.Parallel()
	conn := &blockedTransport{entered: make(chan struct{}, 1), closed: make(chan struct{})}
	c := OverIO(conn)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- c.notify(ctx, "fixture", nil) }()
	<-conn.entered
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("send = %v", err)
	}
	<-c.done
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCanceledSendAdmissionDoesNotWaitForWriter(t *testing.T) {
	t.Parallel()
	conn := &blockedTransport{entered: make(chan struct{}, 1), closed: make(chan struct{})}
	c := OverIO(conn)
	first := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { first <- c.notify(ctx, "first", nil) }()
	<-conn.entered
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if err := c.notify(canceled, "second", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("admission = %v", err)
	}
	cancel()
	<-first
	_ = c.Shutdown(context.Background())
}

func TestMalformedFrameClosesTransport(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	c := OverIOWithOptions(client, TransportOptions{MaxHeaderLineBytes: 8})
	t.Cleanup(func() { _ = server.Close(); _ = c.Shutdown(context.Background()) })
	_, _ = io.WriteString(server, strings.Repeat("x", 9))
	<-c.done
	if err := c.notify(context.Background(), "fixture", nil); !errors.Is(err, ErrServerDied) {
		t.Fatalf("closed session = %v", err)
	}
}

func TestServerProcessExitIsReapedBeforeShutdown(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("subprocess fixture")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c, err := Start(ctx, executable, "-test.run=TestLSPExitHelper", "--", "pan-lsp-exit")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.done:
	case <-ctx.Done():
		t.Fatal("process exit did not close transport")
	}
	if err := c.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestLSPExitHelper(t *testing.T) {
	t.Parallel()
	if os.Args[len(os.Args)-1] == "pan-lsp-exit" {
		os.Exit(0)
	}
}
