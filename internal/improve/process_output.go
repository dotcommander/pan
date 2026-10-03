package improve

import (
	"context"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/ownedprocess"
	"os/exec"
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
	return ownedprocess.Run(ctx, cmd, improveOutputLimits(ctx))
}
