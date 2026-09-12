package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/pipeline/seed"
	"github.com/dotcommander/pan/internal/pipeline/serve"
	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// FlowServeCmd is `pan flow serve`.
type FlowServeCmd struct {
	Paths []string `arg:"" optional:"" help:"Pipeline spec files or directories (default: user pipeline directory)."`
	Port  int      `name:"port" short:"p" default:"7777" help:"TCP port to listen on."`
	Bind  string   `name:"bind" help:"Interface or hostname to listen on (default: loopback)."`
	Open  bool     `name:"open" help:"Open the served page in the default browser."`
}

// Run resolves paths, seeds the empty default directory, and serves pages until cancellation.
func (c FlowServeCmd) Run(_ *kong.Context, _ *Root, deps Deps, ctx context.Context) error {
	paths, err := c.resolvePaths()
	if err != nil {
		return err
	}
	cfg := serve.Config{Paths: paths, Port: c.Port, Bind: c.Bind, Stdout: deps.Out}
	if c.Open {
		cfg.OpenBrowser = openBrowser
	}
	return serve.Run(ctx, cfg)
}

func (c FlowServeCmd) resolvePaths() ([]string, error) {
	if len(c.Paths) > 0 {
		return resolveServePaths(c.Paths)
	}
	return defaultServePaths()
}

func resolveServePaths(values []string) ([]string, error) {
	paths := make([]string, 0, len(values))
	for _, path := range values {
		resolved, err := spec.Resolve(path)
		if err != nil {
			return nil, err
		}
		paths = append(paths, resolved)
	}
	return paths, nil
}

func defaultServePaths() ([]string, error) {
	dir, err := spec.DataDir()
	if err != nil {
		return nil, err
	}
	empty, err := seed.IsEmptyDir(dir)
	if err != nil {
		return nil, err
	}
	if !empty {
		return []string{dir}, nil
	}
	if _, err := seed.Seed(dir, false); err != nil {
		return nil, err
	}
	return []string{dir}, nil
}

func openBrowser(target string) {
	validated, err := browserTarget(target)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "open browser: %v\n", err)
		return
	}
	program, arguments := browserProgram()
	path, err := exec.LookPath(program)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "open browser: %v\n", err)
		return
	}
	command := &exec.Cmd{Path: path, Args: append(append([]string{path}, arguments...), validated)}
	if err := command.Start(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "open browser: %v\n", err)
		return
	}
	go func() { _ = command.Wait() }()
}

func browserProgram() (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		return "xdg-open", nil
	}
}

func browserTarget(target string) (string, error) {
	if target == "" || strings.ContainsRune(target, '\x00') {
		return "", errors.New("invalid browser target")
	}
	if filepath.IsAbs(target) {
		return target, nil
	}
	parsed, err := url.ParseRequestURI(target)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("invalid browser target")
	}
	return parsed.String(), nil
}
