// Package ownedprocess runs a process tree with one waiter and bounded capture.
package ownedprocess

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

var ErrOutputLimit = errors.New("process output limit exceeded")

type Process struct {
	cmd       *exec.Cmd
	done      chan struct{}
	err       error
	terminate func() error
	release   func()
	once      sync.Once
}

func Start(ctx context.Context, cmd *exec.Cmd) (*Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = time.Second
	}
	terminate, release, err := launch(cmd)
	if err != nil {
		return nil, err
	}
	p := &Process{cmd: cmd, done: make(chan struct{}), terminate: terminate, release: release}
	// Wait is called exactly once; termination also closes inherited descendant pipes.
	go func() { p.err = cmd.Wait(); _ = p.Stop(); p.release(); close(p.done) }()
	// This observer exits on completion or cancellation.
	go func() {
		select {
		case <-ctx.Done():
			_ = p.Stop()
		case <-p.done:
		}
	}()
	return p, nil
}
func (p *Process) Stop() error { var err error; p.once.Do(func() { err = p.terminate() }); return err }
func (p *Process) Wait() error { <-p.done; return p.err }

type Limits struct{ StdoutBytes, StderrBytes, CombinedBytes int }
type Result struct {
	Stdout, Stderr  []byte
	StderrTruncated bool
}
type capture struct {
	mu        sync.Mutex
	limit     int
	data      []byte
	truncated bool
	overflow  bool
	cancel    context.CancelFunc
	total     *captureBudget
	fatal     bool
}
type captureBudget struct {
	mu          sync.Mutex
	limit, used int
}

func (c *capture) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	allowed := len(data)
	if c.total != nil {
		c.total.mu.Lock()
		remaining := c.total.limit - c.total.used
		if allowed > remaining {
			allowed = remaining
		}
		c.total.used += allowed
		c.total.mu.Unlock()
	}
	remaining := c.limit - len(c.data)
	if allowed > remaining {
		allowed = remaining
	}
	if allowed < 0 {
		allowed = 0
	}
	c.data = append(c.data, data[:allowed]...)
	if allowed < len(data) {
		c.truncated = true
		if c.fatal {
			c.overflow = true
			c.cancel()
		}
	}
	return len(data), nil
}
func Run(ctx context.Context, cmd *exec.Cmd, limits Limits) (Result, error) {
	if limits.StdoutBytes < 0 || limits.StderrBytes < 0 || limits.CombinedBytes < 0 {
		return Result{}, errors.New("negative process output limit")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &capture{limit: limits.StdoutBytes, cancel: cancel, fatal: true}
	stderr := &capture{limit: limits.StderrBytes, cancel: cancel}
	if limits.CombinedBytes > 0 {
		budget := &captureBudget{limit: limits.CombinedBytes}
		stdout.limit = limits.CombinedBytes
		stderr.limit = limits.CombinedBytes
		stdout.total = budget
		stderr.total = budget
		stderr.fatal = true
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	p, err := Start(runCtx, cmd)
	if err != nil {
		return Result{}, err
	}
	err = p.Wait()
	result := Result{Stdout: stdout.data, Stderr: stderr.data, StderrTruncated: stderr.truncated}
	if stdout.overflow || stderr.overflow {
		return result, errors.Join(ErrOutputLimit, err)
	}
	if ctx.Err() != nil {
		return result, errors.Join(ctx.Err(), err)
	}
	return result, err
}
