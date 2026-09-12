package improve

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
)

const maxPrepArtifactReason = 600

type prepArtifactInput struct {
	Target   PrepTarget
	Outcome  string
	Reason   string
	Changes  []FileChange
	Baseline float64
	Current  float64
}

type prepFailureArtifact struct {
	Schema           string    `json:"schema"`
	Timestamp        time.Time `json:"timestamp"`
	Target           string    `json:"target"`
	Outcome          string    `json:"outcome"`
	Reason           string    `json:"reason"`
	Files            []string  `json:"files,omitempty"`
	BaselineCoverage float64   `json:"baseline_coverage"`
	FinalCoverage    float64   `json:"final_coverage"`
}

func writePrepFailureArtifact(opts PrepOptions, input prepArtifactInput) (string, error) {
	dir := opts.FailureDir
	if dir == "" && opts.StateDir != "" {
		dir = filepath.Join(opts.StateDir, "failures", "prep")
	}
	if dir == "" {
		return "", nil
	}
	packet := prepFailureArtifact{
		Schema: "pan.improve-prep-failure/v1", Timestamp: opts.Now(), Target: input.Target.File,
		Outcome: input.Outcome, Reason: scrubPrepArtifactReason(input.Reason), BaselineCoverage: input.Baseline, FinalCoverage: input.Current,
	}
	for _, change := range input.Changes {
		packet.Files = append(packet.Files, change.FilePath)
	}
	data, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode prep failure packet: %w", err)
	}
	name := fmt.Sprintf("%s-%s-%d.json", sanitizePrepArtifactName(input.Target.File), sanitizePrepArtifactName(input.Outcome), opts.Now().UnixNano())
	path := filepath.Join(dir, name)
	if err := atomicfile.WriteNew(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("store prep failure packet: %w", err)
	}
	return path, nil
}

func scrubPrepArtifactReason(reason string) string {
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) <= maxPrepArtifactReason {
		return reason
	}
	return reason[:maxPrepArtifactReason] + "...<truncated>"
}

func sanitizePrepArtifactName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, value)
}
