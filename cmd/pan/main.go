package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotcommander/pan/internal/cli"
	"github.com/dotcommander/pan/internal/config"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cli.Run(ctx, os.Args[1:], cli.Deps{LoadConfig: config.Load, LoadReadOnlyConfig: config.LoadReadOnly, Out: os.Stdout, In: os.Stdin}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return cli.ExitCode(err)
	}
	return cli.ExitSuccess
}
