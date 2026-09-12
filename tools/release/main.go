package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(realMain())
}

func realMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runCLI(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		return 1
	}
	return 0
}

func runCLI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	if opts.help {
		printUsage(stdout)
		return nil
	}

	proc := newProcess(stdout, stderr)
	root, err := proc.capture(ctx, "", nil, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	workflow, err := newReleaseWorkflow(ctx, proc, root, opts)
	if err != nil {
		return err
	}
	return workflow.run(ctx)
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: just release VERSION [--publish] [--resume]

Validates and prepares a Pan release. The default is a read-only dry run.
Source and Homebrew tap worktrees must already be reviewed and clean.

  --publish  Execute commit, tag, GitHub release, and Homebrew publication.
  --resume   Resume an interrupted --publish run from its durable receipt.

Examples:
  just release 0.1.0
  just release 0.1.0 --publish
  just release 0.1.0 --publish --resume`)
}
