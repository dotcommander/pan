package improve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotcommander/pan/internal/provider"
)

// ProviderTestGenerator asks the injected completion client for test-only
// whole-file replacements. Prep validates the result before it reaches disk.
type ProviderTestGenerator struct {
	Client   CompletionClient
	Model    string
	Settings ProviderSettings
}

// WithRepository returns a copy bound to the isolated prep worktree.
func (p ProviderTestGenerator) WithRepository(root string) ProviderTestGenerator {
	p.Settings.RepoPath = root
	return p
}

// Generate requests test-only replacements for target using bounded local source.
func (p ProviderTestGenerator) Generate(ctx context.Context, target PrepTarget, feedback string) ([]FileChange, error) {
	if p.Client == nil {
		return nil, errors.New("provider client is required")
	}
	prompt, err := p.prepPrompt(target, feedback)
	if err != nil {
		return nil, err
	}
	system := strings.TrimSpace(p.Settings.PrepFileSystemPrompt)
	if system == "" {
		system = strings.TrimSpace(p.Settings.PrepSystemPrompt)
	}
	if system == "" {
		system = "Generate focused characterization tests. Return JSON only. Do not call tools."
	}
	if !p.Settings.PrepThinkingEnabled {
		system += " Thinking is disabled for this focused request."
	}
	model := p.Model
	if p.Settings.PrepFileModel != "" {
		model = p.Settings.PrepFileModel
	}
	request := provider.Request{Model: model, Messages: []provider.Message{{Role: providerSystemRole, Content: system}, {Role: providerUserRole, Content: prompt}}, ResponseFormat: map[string]string{kindType: "json_object"}}
	if !p.Settings.PrepThinkingEnabled {
		request.ProviderOptions = map[string]any{"thinking": map[string]any{kindType: "disabled"}}
	}
	response, err := p.Client.Complete(ctx, request)
	if err != nil {
		return nil, err
	}
	if len(response.Choices) != 1 {
		return nil, fmt.Errorf("provider returned %d choices; exactly one is required", len(response.Choices))
	}
	var packet struct {
		Changes []FileChange `json:"changes"`
	}
	if err := json.Unmarshal([]byte(response.Choices[0].Message.Content), &packet); err != nil {
		return nil, fmt.Errorf("decode test generation: %w", err)
	}
	if len(packet.Changes) == 0 {
		return nil, errors.New("provider returned no test changes")
	}
	return packet.Changes, nil
}

func (p ProviderTestGenerator) prepPrompt(target PrepTarget, feedback string) (string, error) {
	prompt := strings.TrimSpace(p.Settings.PrepPrompt)
	if prompt == "" {
		prompt = "Generate focused Go test file replacements."
	}
	prompt += fmt.Sprintf("\nTarget: %s (coverage %.2f%%). Return JSON {changes:[{file_path,new_contents,reasoning}]}. Only _test.go files are allowed.", target.File, target.CoveragePercent)
	if p.Settings.RepoPath == "" {
		return prompt + "\n" + feedback, nil
	}
	reader, err := newProviderReader(p.Settings.RepoPath, p.Settings.Exclude, p.Settings.prepSourceBytes())
	if err != nil {
		return "", err
	}
	full, rel, err := reader.path(target.File)
	if err != nil {
		return "", fmt.Errorf("read prep target: %w", err)
	}
	source, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("read prep target: %w", err)
	}
	if len(source) > p.Settings.prepSourceBytes() {
		source = source[:p.Settings.prepSourceBytes()]
	}
	context, err := prepPackageSource(reader, filepath.Dir(rel), rel, p.Settings.prepContextBytes())
	if err != nil {
		return "", err
	}
	return prompt + "\n\n=== TARGET SOURCE: " + rel + " ===\n" + string(source) + "\n\n=== PACKAGE CONTEXT ===\n" + context + prepContext(target, p.Settings) + "\n\n=== FEEDBACK ===\n" + feedback, nil
}

func prepContext(target PrepTarget, settings ProviderSettings) string {
	var b strings.Builder
	if max := settings.PrepPromptTargetFuncs; max > 0 {
		b.WriteString("\n\n=== TARGET FUNCTIONS ===\n")
		b.WriteString(strings.Join(goFunctionNames(target.File, settings.RepoPath, max), "\n"))
	}
	if max := settings.PrepPromptCoverageGaps; max > 0 && len(target.Gaps) > 0 {
		b.WriteString("\n\n=== COVERAGE GAPS ===\n")
		for i, gap := range target.Gaps {
			if i == max {
				break
			}
			fmt.Fprintf(&b, "%d-%d (%d statements)\n", gap.StartLine, gap.EndLine, gap.Statements)
		}
	}
	return b.String()
}
