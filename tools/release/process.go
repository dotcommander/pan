package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

type process struct {
	stdout io.Writer
	stderr io.Writer
}

func newProcess(stdout, stderr io.Writer) *process {
	return &process{stdout: stdout, stderr: stderr}
}

func (p *process) capture(ctx context.Context, dir string, env map[string]string, name string, args ...string) (string, error) {
	output, code, err := p.captureStatus(ctx, dir, env, name, args...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("%s exited %d: %s", commandString(name, args), code, strings.TrimSpace(output))
	}
	return strings.TrimSpace(output), nil
}

func (p *process) captureStatus(ctx context.Context, dir string, env map[string]string, name string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = commandEnv(env)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(output), exitErr.ExitCode(), nil
	}
	return string(output), -1, fmt.Errorf("start %s: %w", commandString(name, args), err)
}

func (p *process) captureBytes(ctx context.Context, dir string, env map[string]string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = commandEnv(env)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", commandString(name, args), err, strings.TrimSpace(stderr.String()))
	}
	return output, nil
}

func (p *process) stream(ctx context.Context, dir string, env map[string]string, name string, args ...string) error {
	fmt.Fprintln(p.stdout, "$", commandString(name, args))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = commandEnv(env)
	cmd.Stdout = p.stdout
	cmd.Stderr = p.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", commandString(name, args), err)
	}
	return nil
}

func commandEnv(overrides map[string]string) []string {
	env := os.Environ()
	for key, value := range overrides {
		prefix := key + "="
		env = slices.DeleteFunc(env, func(item string) bool { return strings.HasPrefix(item, prefix) })
		env = append(env, prefix+value)
	}
	return env
}

func commandString(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, strconv.Quote(name))
	for _, arg := range args {
		parts = append(parts, strconv.Quote(arg))
	}
	return strings.Join(parts, " ")
}

func requireTools(names ...string) error {
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("required tool %q not found", name)
		}
	}
	return nil
}
