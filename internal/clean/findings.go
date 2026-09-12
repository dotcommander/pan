package clean

import (
	"fmt"
	"path/filepath"
)

// Finding producer labels.
const (
	sourceWalker   = "walker"
	sourceGit      = "git"
	sourceClassify = "classify"
)

// EmitFindings converts enriched FileInfo fields into normalized findings.
// Call after walking, git enrichment, classification, and duplicate
// detection so the plan's "why" is fully populated.
func EmitFindings(files []FileInfo, largeFileBytes int64) {
	for i := range files {
		f := &files[i]
		emitInventoryFindings(f)
		emitStaleFindings(f)
		emitSizeFindings(f, largeFileBytes)
		emitContentFindings(f)
		emitStateFindings(f)
	}
}

// emitInventoryFindings records the walk-derived inventory signals.
func emitInventoryFindings(f *FileInfo) {
	if !f.GitStateUnknown && !f.Tracked {
		f.AddFinding(Finding{Source: sourceWalker, Rule: RuleUntracked, Severity: SevInfo, Confidence: 1.0, Message: "not tracked by git"})
	}
	if f.IsEmpty {
		f.AddFinding(Finding{Source: sourceWalker, Rule: RuleEmpty, Severity: SevInfo, Confidence: 1.0, Message: "empty directory"})
	}
}

func emitStaleFindings(f *FileInfo) {
	switch {
	case f.StaleDays > 365:
		f.AddFinding(Finding{Source: sourceGit, Rule: RuleStale, Severity: SevError, Confidence: 1.0, Message: fmt.Sprintf("stale %d days", f.StaleDays)})
	case f.StaleDays > 180:
		f.AddFinding(Finding{Source: sourceGit, Rule: RuleStale, Severity: SevWarn, Confidence: 0.8, Message: fmt.Sprintf("stale %d days", f.StaleDays)})
	case f.StaleDays > 90:
		f.AddFinding(Finding{Source: sourceGit, Rule: RuleStale, Severity: SevInfo, Confidence: 0.6, Message: fmt.Sprintf("stale %d days", f.StaleDays)})
	}
}

func emitSizeFindings(f *FileInfo, largeFileBytes int64) {
	switch {
	case f.Size > 100*1024*1024:
		f.AddFinding(Finding{Source: sourceWalker, Rule: RuleLargeFile, Severity: SevError, Confidence: 1.0, Message: fmt.Sprintf("file size %dMB", f.Size/1024/1024)})
	case f.Size > largeFileBytes && largeFileBytes > 0:
		f.AddFinding(Finding{Source: sourceWalker, Rule: RuleLargeFile, Severity: SevWarn, Confidence: 1.0, Message: fmt.Sprintf("file size %dMB", f.Size/1024/1024)})
	}
}

func emitContentFindings(f *FileInfo) {
	switch f.Content {
	case ContentGenerated:
		f.AddFinding(Finding{Source: sourceClassify, Rule: RuleGenerated, Severity: SevInfo, Confidence: 0.8, Message: "generated content detected"})
	case ContentScratch:
		f.AddFinding(Finding{Source: sourceClassify, Rule: RuleScratch, Severity: SevInfo, Confidence: 0.7, Message: "scratch/notes content"})
	case ContentTodoOnly:
		f.AddFinding(Finding{Source: sourceClassify, Rule: RuleTodoOnly, Severity: SevInfo, Confidence: 0.7, Message: "todo-only content"})
	case ContentLogDump:
		f.AddFinding(Finding{Source: sourceClassify, Rule: RuleLogDump, Severity: SevInfo, Confidence: 0.7, Message: "log dump content"})
	case ContentUnknown, ContentConfig, ContentMeaningful:
		// These classes carry no content-based signal.
	}
}

// emitStateFindings records the git-history and duplicate signals.
func emitStateFindings(f *FileInfo) {
	if f.Orphaned {
		f.AddFinding(Finding{Source: sourceGit, Rule: RuleOrphaned, Severity: SevWarn, Confidence: 0.9, Message: "deleted from recent git history"})
	}
	if f.Duplicate != "" {
		f.AddFinding(Finding{Source: "duplicates", Rule: RuleDuplicate, Severity: SevWarn, Confidence: 0.8, Message: "duplicate of " + filepath.ToSlash(f.Duplicate)})
	}
}
