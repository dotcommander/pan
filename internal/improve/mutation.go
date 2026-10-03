package improve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/dotcommander/pan/internal/ownedprocess"
	"os/exec"
	"strconv"
	"strings"
)

// MutationRunner measures whether the changed packages' tests kill mutations.
// Scores are fractions in [0,1]. The runner is injected so normal improve
// commands do not require a mutation-testing tool to be installed.
type MutationRunner interface {
	Run(context.Context, string, []string) (float64, error)
}

// CommandMutationRunner invokes an explicitly configured runner with direct
// argv. The program receives the repository and package list as flags and must
// print either a JSON object containing score or one numeric score (0..1).
type CommandMutationRunner struct{ Path string }

// Run invokes the configured executable and parses its mutation score.
func (r CommandMutationRunner) Run(ctx context.Context, dir string, packages []string) (float64, error) {
	if strings.TrimSpace(r.Path) == "" {
		return 0, errors.New("mutation runner path is empty")
	}
	executable, err := exec.LookPath(r.Path)
	if err != nil {
		return 0, fmt.Errorf("find mutation runner: %w", err)
	}
	args := append([]string{"--repo", dir}, "--packages")
	args = append(args, packages...)
	cmd := &exec.Cmd{Path: executable, Args: append([]string{executable}, args...), Dir: dir}
	captured, err := captureImproveCommand(ctx, cmd)
	if err != nil {
		return 0, fmt.Errorf("mutation runner: %w: %s", err, strings.TrimSpace(string(captured.Stderr)))
	}
	return parseMutationScore(string(captured.Stdout))
}

// runMutationCommand preserves the cancellation seam for callers supplying
// their own writers while retaining tree ownership and one process waiter.
func runMutationCommand(ctx context.Context, cmd *exec.Cmd) error {
	process, err := ownedprocess.Start(ctx, cmd)
	if err != nil {
		return err
	}
	err = process.Wait()
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), err)
	}
	return err
}

func parseMutationScore(output string) (float64, error) {
	trimmed := strings.TrimSpace(output)
	var packet struct {
		Score *float64 `json:"score"`
	}
	if json.Unmarshal([]byte(trimmed), &packet) == nil && packet.Score != nil {
		return checkedMutationScore(*packet.Score)
	}
	score, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, errors.New("mutation runner returned no numeric score")
	}
	return checkedMutationScore(score)
}

func checkedMutationScore(score float64) (float64, error) {
	if score < 0 || score > 1 {
		return 0, fmt.Errorf("mutation score %.4f is outside [0,1]", score)
	}
	return score, nil
}
