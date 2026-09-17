// Package checks runs bounded, trusted repository checks without executing
// repository-provided code.
package checks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const Schema = "pan.scan.checks/v1"

const maxSkillBytes int64 = 1 << 20

type Status string

const (
	StatusPassed  Status = "passed"
	StatusFailed  Status = "failed"
	StatusSkipped Status = "skipped"
)

type Finding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
}

type CheckResult struct {
	ID       string    `json:"id"`
	Status   Status    `json:"status"`
	Findings []Finding `json:"findings"`
	Reason   string    `json:"reason,omitempty"`
}

type Coverage struct {
	Complete  bool     `json:"complete"`
	Inspected []string `json:"inspected"`
	Skipped   []string `json:"skipped"`
	Limits    []string `json:"limits"`
}

type Report struct {
	Schema   string        `json:"schema"`
	Target   string        `json:"target"`
	Checks   []CheckResult `json:"checks"`
	Coverage Coverage      `json:"coverage"`
}

type Descriptor struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type Request struct {
	Root     string
	Target   string
	CheckIDs []string
}

type Provider interface {
	Descriptor() Descriptor
	Applicable(target string, info os.FileInfo) (bool, string, error)
	Run(ctx context.Context, root, target string, info os.FileInfo) (CheckResult, error)
}

type Registry struct {
	providers map[string]Provider
	ordered   []Provider
}

func NewRegistry(providers ...Provider) (*Registry, error) {
	r := &Registry{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		if provider == nil {
			return nil, errors.New("check provider must not be nil")
		}
		id := provider.Descriptor().ID
		if id == "" {
			return nil, errors.New("check provider ID must not be empty")
		}
		if _, exists := r.providers[id]; exists {
			return nil, fmt.Errorf("duplicate check provider ID %q", id)
		}
		r.providers[id] = provider
		r.ordered = append(r.ordered, provider)
	}
	sort.Slice(r.ordered, func(i, j int) bool {
		return r.ordered[i].Descriptor().ID < r.ordered[j].Descriptor().ID
	})
	return r, nil
}

func Builtins() *Registry {
	registry, err := NewRegistry(skillContractProvider{})
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) List() []Descriptor {
	result := make([]Descriptor, 0, len(r.ordered))
	for _, provider := range r.ordered {
		result = append(result, provider.Descriptor())
	}
	return result
}

func (r *Registry) Run(ctx context.Context, request Request) (Report, error) {
	root, target, display, info, err := resolveTarget(request.Root, request.Target)
	if err != nil {
		return Report{}, err
	}
	providers, err := r.selectProviders(request.CheckIDs)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		Schema: Schema,
		Target: display,
		Checks: make([]CheckResult, 0, len(providers)),
		Coverage: Coverage{
			Complete:  true,
			Inspected: []string{},
			Skipped:   []string{},
			Limits:    []string{fmt.Sprintf("skill files are limited to %d bytes", maxSkillBytes)},
		},
	}
	for _, provider := range providers {
		id := provider.Descriptor().ID
		applicable, reason, applicableErr := provider.Applicable(target, info)
		if applicableErr != nil {
			return Report{}, fmt.Errorf("check %s applicability: %w", id, applicableErr)
		}
		if !applicable {
			report.Checks = append(report.Checks, CheckResult{ID: id, Status: StatusSkipped, Findings: []Finding{}, Reason: reason})
			report.Coverage.Skipped = append(report.Coverage.Skipped, id)
			continue
		}
		result, runErr := provider.Run(ctx, root, target, info)
		if runErr != nil {
			return Report{}, fmt.Errorf("check %s: %w", id, runErr)
		}
		result.ID = id
		if result.Findings == nil {
			result.Findings = []Finding{}
		}
		report.Checks = append(report.Checks, result)
		report.Coverage.Inspected = append(report.Coverage.Inspected, id)
	}
	return report, nil
}

func (r *Registry) selectProviders(ids []string) ([]Provider, error) {
	if len(ids) == 0 {
		return append([]Provider(nil), r.ordered...), nil
	}
	seen := map[string]bool{}
	providers := make([]Provider, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		provider, ok := r.providers[id]
		if !ok {
			return nil, fmt.Errorf("unknown check %q", id)
		}
		if !seen[id] {
			seen[id] = true
			providers = append(providers, provider)
		}
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Descriptor().ID < providers[j].Descriptor().ID })
	return providers, nil
}

func resolveTarget(root, target string) (resolvedRoot, resolvedTarget, display string, info os.FileInfo, err error) {
	resolvedRoot, err = filepath.Abs(root)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("resolve repository root: %w", err)
	}
	resolvedRoot, err = filepath.EvalSymlinks(resolvedRoot)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("resolve repository root: %w", err)
	}
	if target == "" {
		target = "."
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(resolvedRoot, target)
	}
	resolvedTarget, err = filepath.EvalSymlinks(target)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("resolve check target %q: %w", target, err)
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("relativize check target: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", "", nil, fmt.Errorf("check target %q is outside repository root", target)
	}
	info, err = os.Stat(resolvedTarget)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("stat check target: %w", err)
	}
	display = filepath.ToSlash(rel)
	if display == "." {
		display = "."
	}
	return resolvedRoot, resolvedTarget, display, info, nil
}
