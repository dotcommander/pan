package improve

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

func decodeProviderDeletions(reader *providerReader, raw string) (*Proposal, error) {
	var request struct {
		Rationale string `json:"rationale"`
		Deletions []struct {
			FilePath  string   `json:"file_path"`
			Symbols   []string `json:"symbols"`
			Reasoning string   `json:"reasoning"`
		} `json:"deletions"`
	}
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		return nil, fmt.Errorf("decode provider deletions: %w", err)
	}
	if strings.TrimSpace(request.Rationale) == "" || len(request.Deletions) == 0 {
		return nil, errors.New("provider deletions require rationale and entries")
	}
	changes := make([]FileChange, 0, len(request.Deletions))
	for _, deletion := range request.Deletions {
		full, rel, err := reader.path(deletion.FilePath)
		if err != nil {
			return nil, err
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil, fmt.Errorf("deletion target %q must be a non-test Go file", deletion.FilePath)
		}
		if len(deletion.Symbols) == 0 {
			return nil, fmt.Errorf("deletion target %q has no symbols", deletion.FilePath)
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		names := make(map[string]bool, len(deletion.Symbols))
		for _, name := range deletion.Symbols {
			if strings.TrimSpace(name) == "" {
				return nil, errors.New("deletion symbol is empty")
			}
			names[name] = true
		}
		out, dropped := removeDeclarations(data, names)
		if len(dropped) != len(names) {
			return nil, fmt.Errorf("deletion symbols not found in %q", deletion.FilePath)
		}
		changes = append(changes, FileChange{FilePath: rel, NewContents: string(out), Reasoning: deletion.Reasoning})
	}
	return &Proposal{Rationale: request.Rationale, Changes: changes}, nil
}
