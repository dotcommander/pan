package improve

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/ownedprocess"
)

type outputLimitsKey struct{}

func withOutputLimits(ctx context.Context, rules config.ImproveRules) context.Context {
	return context.WithValue(ctx, outputLimitsKey{}, rules.Normalized())
}

func improveOutputLimits(ctx context.Context) ownedprocess.Limits {
	rules, _ := ctx.Value(outputLimitsKey{}).(config.ImproveRules)
	rules = rules.Normalized()
	return ownedprocess.Limits{StdoutBytes: rules.MaxStdoutBytes, StderrBytes: rules.MaxStderrBytes}
}

func captureImproveCommand(ctx context.Context, cmd *exec.Cmd) (ownedprocess.Result, error) {
	limits := improveOutputLimits(ctx)
	result, err := ownedprocess.Run(ctx, cmd, limits)
	if errors.Is(err, ownedprocess.ErrOutputLimit) {
		return result, fmt.Errorf("increase improve.max_stdout_bytes (currently %d bytes): %w", limits.StdoutBytes, err)
	}
	return result, err
}
