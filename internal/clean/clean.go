package clean

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
)

// Resolved returns a copy of the options with an absolute, symlink-resolved
// root, normalized rules, and normalized exclusions. Every entry point
// resolves defensively; the operation is idempotent, so callers may thread
// one resolved Options from analysis through apply.
func (o Options) Resolved() (Options, error) {
	abs, err := filepath.Abs(o.Root)
	if err != nil {
		return o, fmt.Errorf("resolve root: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	o.Root = abs
	o.Rules = o.Rules.Normalized()
	o.Exclude = normalizeExcludes(o.Exclude)
	return o, nil
}

// Analyze runs one full read-only clean pass: walk, git enrichment,
// content classification, duplicate detection, findings emission, and
// categorization. The returned Analysis carries the enriched inventory and
// the plan; nothing on disk is modified.
func Analyze(ctx context.Context, opts Options) (Analysis, error) {
	opts, err := opts.Resolved()
	if err != nil {
		return Analysis{}, err
	}
	files, err := Walk(opts)
	if err != nil {
		return Analysis{}, err
	}

	info := loadGitInfo(ctx, opts.Root, opts)
	info.loadHistory(ctx, opts.Root, opts, files)
	info.mark(files, opts)

	Classify(files)
	FindDuplicates(files)
	EmitFindings(files, opts.largeFileBytes())
	plan := Categorize(files, opts)
	plan.Path = opts.Root
	plan.GitAvailable = info.available
	if !info.available {
		cause := info.cause
		if len(cause) > 200 {
			cause = cause[:200]
		}
		plan.Note = fmt.Sprintf("git unavailable (%s); repository state unknown and apply is blocked", cause)
	}
	return Analysis{Files: files, Plan: plan}, nil
}

// Missing runs the walk and the completeness check only. It needs no git
// state: the checklist inspects walked paths.
func Missing(ctx context.Context, opts Options) (CompletenessReport, error) {
	opts, err := opts.Resolved()
	if err != nil {
		return CompletenessReport{}, err
	}
	files, err := Walk(opts)
	if err != nil {
		return CompletenessReport{}, err
	}
	report := CheckCompleteness(files, opts.Rules)
	report.Schema = MissingSchema
	report.Path = opts.Root
	return report, nil
}

// Findings builds the signal-grouped report from one full analysis pass.
func Findings(ctx context.Context, opts Options) (FindingsReport, error) {
	analysis, err := Analyze(ctx, opts)
	if err != nil {
		return FindingsReport{}, err
	}
	report := FindingsReport{Schema: FindingsSchema, Path: analysis.Plan.Path, GitAvailable: analysis.Plan.GitAvailable}

	type key struct{ rule, file string }
	seen := map[key]bool{}
	for i := range analysis.Files {
		f := &analysis.Files[i]
		if len(f.Findings) == 0 {
			continue
		}
		report.FilesWithSignals++
		for _, finding := range f.Findings {
			k := key{finding.Rule, f.RelPath}
			if seen[k] {
				continue
			}
			seen[k] = true
			// Groups are appended in first-seen order and sorted below.
			report.Groups = appendGroup(report.Groups, finding.Rule, FindingEntry{
				Severity: severityLabel(finding.Severity),
				File:     filepath.ToSlash(f.RelPath),
				SizeKB:   f.Size / 1024,
				Score:    Score(f),
				Message:  finding.Message,
			})
		}
	}
	sort.Slice(report.Groups, func(i, j int) bool {
		if report.Groups[i].Count != report.Groups[j].Count {
			return report.Groups[i].Count > report.Groups[j].Count
		}
		return report.Groups[i].Rule < report.Groups[j].Rule
	})
	for i := range report.Groups {
		entries := report.Groups[i].Entries
		sort.Slice(entries, func(a, b int) bool { return entries[a].File < entries[b].File })
	}
	return report, nil
}

// appendGroup appends one entry to its rule group, creating the group when
// needed.
func appendGroup(groups []FindingGroup, rule string, entry FindingEntry) []FindingGroup {
	for i := range groups {
		if groups[i].Rule == rule {
			groups[i].Entries = append(groups[i].Entries, entry)
			groups[i].Count++
			return groups
		}
	}
	return append(groups, FindingGroup{Rule: rule, Count: 1, Entries: []FindingEntry{entry}})
}

func severityLabel(severity int) string {
	switch {
	case severity >= SevError:
		return sevLabelError
	case severity == SevWarn:
		return "warn"
	default:
		return sevLabelInfo
	}
}
