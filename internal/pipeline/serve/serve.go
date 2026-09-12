// Package serve loads pipeline specs and serves live-reloading HTML pages.
package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

const pollInterval = 150 * time.Millisecond

// Config controls a serve invocation.
type Config struct {
	Paths       []string
	Port        int
	Bind        string
	OpenBrowser func(string)
	Stdout      io.Writer
}

// Run serves the requested file and directory collections until ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	plans, err := expandPaths(cfg.Paths)
	if err != nil {
		return err
	}
	if len(plans) == 0 {
		return errors.New("no collections produced from the specified paths")
	}
	set := newCollectionSet()
	states, err := loadPlans(set, plans)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", makeHandler(set))
	server := newServer(cfg.Bind, cfg.Port, mux)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer func() { _ = listener.Close() }()
	baseURL := "http://" + listener.Addr().String() + "/"
	out := cfg.Stdout
	if out == nil {
		out = os.Stdout
	}
	_, _ = fmt.Fprintf(out, "serving %s — %d collection(s)\n", baseURL, len(set.summaries()))
	if cfg.OpenBrowser != nil {
		cfg.OpenBrowser(baseURL)
	}

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		poll(runCtx, set, plans, states)
	}()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-runCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(runCtx), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	err = server.Serve(listener)
	stop()
	<-watchDone
	<-shutdownDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

func newServer(bind string, port int, handler http.Handler) *http.Server {
	if bind == "" {
		bind = net.IPv4(127, 0, 0, 1).String()
	}
	timeout := 5 * time.Second
	return &http.Server{Addr: net.JoinHostPort(bind, strconv.Itoa(port)), Handler: handler, ReadHeaderTimeout: timeout, ReadTimeout: timeout, WriteTimeout: timeout, IdleTimeout: 3 * timeout}
}
