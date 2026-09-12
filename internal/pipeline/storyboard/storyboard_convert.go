package storyboard

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

func (sources sourceContext) phasesFromSpec(phases []spec.Phase) []Phase {
	out := make([]Phase, 0, len(phases))
	for i, p := range phases {
		out = append(out, Phase{
			Ordinal:        i + 1,
			Name:           p.Name,
			Kind:           string(p.Kind),
			Goal:           p.Description,
			Files:          append([]string(nil), p.Files...),
			Stages:         sources.stagesFromSpec(p.Stages),
			TruncatedCount: p.TruncatedCount,
		})
	}
	return out
}

func (sources sourceContext) stagesFromSpec(stages []spec.Stage) []Stage {
	out := make([]Stage, 0, len(stages))
	for _, st := range stages {
		switch {
		case st.Chip != nil:
			label := st.Chip.Label
			if subtitle := strings.TrimSpace(st.Chip.Subtitle); subtitle != "" {
				label += " - " + subtitle
			}
			out = append(out, sources.sourceStage(label, st.Chip.SourceFile, st.Chip.SourceLine))
		case st.Fork != nil:
			out = append(out, Stage{Label: "fork: " + st.Fork.Gate, Role: roleRoute})
			for _, branch := range st.Fork.Branches {
				out = append(out, sources.sourceStage(joinLabelParts(branch.Condition, branch.Label), branch.SourceFile, branch.SourceLine))
			}
		case st.Fanout != nil:
			out = append(out, Stage{Label: "fanout: " + st.Fanout.Gate, Role: roleRoute})
			for _, target := range st.Fanout.Targets {
				out = append(out, Stage{Label: joinLabelParts(target.Flag, target.Label), Role: roleRoute})
			}
		case st.External != nil:
			label := "external: " + st.External.Label
			if st.External.Kind != "" {
				label += " (" + st.External.Kind + ")"
			}
			stage := sources.sourceStage(label, st.External.SourceFile, st.External.SourceLine)
			stage.Role = roleIO
			out = append(out, stage)
		}
	}
	return out
}

func joinLabelParts(left, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	switch {
	case left == "":
		return right
	case right == "":
		return left
	default:
		return left + " -> " + right
	}
}

func (sources sourceContext) sourceStage(label, sourceFile string, sourceLine int) Stage {
	stage := Stage{Label: label, Role: inferStageRole(label, sourceFile), SourceFile: sourceFile, SourceLine: sourceLine}
	if sources.sourceRoot != "" && sourceFile != "" && sourceLine > 0 {
		stage.CodeSnippet = readSourceSnippet(sources.sourceRoot, sourceFile, sourceLine)
	}
	return stage
}

func readSourceSnippet(sourceRoot, sourceFile string, sourceLine int) string {
	path, ok := sourcePath(sourceRoot, sourceFile)
	if !ok {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	line := 1
	for scanner.Scan() {
		if line == sourceLine {
			return capSnippet(strings.TrimSpace(scanner.Text()))
		}
		line++
	}
	return ""
}

func sourcePath(sourceRoot, sourceFile string) (string, bool) {
	rel := filepath.Clean(filepath.FromSlash(sourceFile))
	if rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Join(sourceRoot, rel), true
}

func capSnippet(s string) string {
	runes := []rune(s)
	if len(runes) <= maxSnippetLen {
		return s
	}
	return string(runes[:maxSnippetLen-3]) + "..."
}

func storesFromSpec(stores []spec.Store) []Store {
	out := make([]Store, 0, len(stores))
	for _, store := range stores {
		writers := make([]Writer, 0, len(store.Writers))
		for _, writer := range store.Writers {
			writers = append(writers, Writer{
				Stage:  writer.Stage,
				Access: writer.Access,
				Note:   writer.Note,
			})
		}
		out = append(out, Store{Name: store.Name, Writers: writers})
	}
	return out
}

func coverageFromSpec(c *spec.Coverage) *Coverage {
	if c == nil {
		return nil
	}
	out := &Coverage{
		Represented: c.Represented,
		Total:       c.Total,
		Missing:     append([]string(nil), c.Missing...),
	}
	if c.IsLow() {
		out.Warning = fmt.Sprintf("low scan coverage: %d/%d source files represented", c.Represented, c.Total)
	}
	return out
}
