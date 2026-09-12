// Package clean plans and applies bounded repository cleanup.
//
// Analysis (plan, findings, missing, commands) is strictly read-only. The
// only mutating surface is a confirmed Apply, which backs up every touched
// path into a tar.gz archive, writes a JSON manifest beside it, and only
// then executes deterministic filesystem and git-index actions. Git is
// invoked exclusively through direct argv plumbing commands — a shell is
// never executed — and untracked paths never receive git moves or index
// mutations: they move on the filesystem only.
package clean

import "time"

// Shared path vocabulary for the walk and the path-safety gates.
const (
	parentDir  = ".."
	gitDirName = ".git"
)

// Schema stamps for clean result documents.
const (
	PlanSchema     = "pan.clean-plan/v1"
	FindingsSchema = "pan.clean-findings/v1"
	MissingSchema  = "pan.clean-missing/v1"
	CommandsSchema = "pan.clean-commands/v1"
	ApplySchema    = "pan.clean-apply/v1"
	ManifestSchema = "pan.clean-manifest/v1"
)

// Category keys for plan buckets and summary counts.
const (
	CatDelete       = "delete_candidates"
	CatDevArtifact  = "dev_artifact_candidates"
	CatArchive      = "archive_candidates"
	CatBrokenLink   = "broken_links"
	CatLargeFile    = "large_files"
	CatMisplaced    = "misplaced_scripts"
	CatMisplacedDoc = "misplaced_docs"
	CatUntrack      = "untrack_candidates"
	CatRenameDocs   = "rename_docs"
)

// Severity levels for findings.
const (
	SevInfo  = 0
	SevWarn  = 1
	SevError = 2
)

// Rule names for findings signals.
const (
	RuleUntracked = "untracked"
	RuleStale     = "stale"
	RuleOrphaned  = "orphaned"
	RuleLargeFile = "large-file"
	RuleEmpty     = "empty"
	RuleGenerated = "generated"
	RuleScratch   = "scratch"
	RuleTodoOnly  = "todo-only"
	RuleLogDump   = "log-dump"
	RuleDuplicate = "duplicate"
)

// Status labels for the per-file disposition inventory.
const (
	StatusClean           = "clean"
	StatusDelete          = "delete"
	StatusArchive         = "archive"
	StatusUntrack         = "untrack"
	StatusDevArtifact     = "dev_artifact"
	StatusMisplacedDoc    = "misplaced_doc"
	StatusMisplacedScript = "misplaced_script"
	StatusLargeFile       = "large_file"
	StatusBrokenLink      = "broken_link"
	StatusRenameDoc       = "rename_doc"
)

// ContentClass is the content-aware classification of an ambiguous file.
type ContentClass int

// ContentClass values for the content-aware classification of an
// ambiguous file.
const (
	ContentUnknown ContentClass = iota
	ContentGenerated
	ContentScratch
	ContentTodoOnly
	ContentLogDump
	ContentConfig
	ContentMeaningful
)

// contentHintUnknown is the shared spelling of the unknown content hint,
// used by ContentClass.String and the consumers that compare against it.
const contentHintUnknown = "unknown"

const (
	contentHintConfig = "config"
	makefileName      = "Makefile"
)

// String returns the lowercase name for a ContentClass.
func (c ContentClass) String() string {
	switch c {
	case ContentUnknown:
		return contentHintUnknown
	case ContentGenerated:
		return "generated"
	case ContentScratch:
		return "scratch"
	case ContentTodoOnly:
		return "todo_list"
	case ContentLogDump:
		return "log_dump"
	case ContentConfig:
		return contentHintConfig
	case ContentMeaningful:
		return "meaningful"
	default:
		return contentHintUnknown
	}
}

// Finding is one normalized signal produced by any analyzer.
type Finding struct {
	Source     string  `json:"source"`     // producer: "git", "classify", "duplicates", "walker"
	Rule       string  `json:"rule"`       // signal name from Rule constants
	Severity   int     `json:"severity"`   // SevInfo, SevWarn, SevError
	Confidence float64 `json:"confidence"` // 0.0-1.0
	Message    string  `json:"message"`    // human-readable
}

// FileInfo is the internal enriched representation of one walked path.
// RelPath is relative to the scan root; candidate and action paths are
// emitted slash-separated.
type FileInfo struct {
	Path            string // absolute
	RelPath         string // relative to the scan root
	Size            int64
	IsDir           bool
	IsSymlink       bool
	IsEmpty         bool // empty directory
	Tracked         bool
	GitStateUnknown bool // repository index or ignore state could not be read
	Ignored         bool // matched by git ignore rules
	StaleDays       int  // 0 = recently touched
	ModTime         time.Time
	Content         ContentClass
	LinkTarget      string // for symlinks, readlink value
	Duplicate       string // if set, path of the original this duplicates
	Executable      bool
	Orphaned        bool // deleted from recent git history but still on disk
	Findings        []Finding
	Suppressed      bool // matched by the clean ignore file
}

// AddFinding appends a finding to the file's signal list.
func (f *FileInfo) AddFinding(finding Finding) {
	f.Findings = append(f.Findings, finding)
}

// HasFinding reports whether the file carries a finding with the given rule.
func (f *FileInfo) HasFinding(rule string) bool {
	for _, finding := range f.Findings {
		if finding.Rule == rule {
			return true
		}
	}
	return false
}

// Candidate is the enriched per-file entry inside a Plan bucket.
type Candidate struct {
	File        string    `json:"file"`
	Reason      string    `json:"reason,omitempty"`
	SizeKB      int64     `json:"size_kb"`
	Tracked     *bool     `json:"tracked,omitempty"`
	Referenced  *bool     `json:"referenced,omitempty"`
	Target      string    `json:"target,omitempty"`
	StaleDays   int       `json:"stale_days,omitempty"`
	Score       int       `json:"score,omitempty"`
	ContentHint string    `json:"content_hint,omitempty"`
	Findings    []Finding `json:"findings,omitempty"`
}

// LabeledFile tags every walked file with a disposition status.
type LabeledFile struct {
	File   string `json:"file"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	SizeKB int64  `json:"size_kb"`
}

// Plan is the top-level cleanup plan: every candidate bucket, a health
// score, a per-file disposition inventory, and summary counts.
type Plan struct {
	Schema                string         `json:"schema"`
	Path                  string         `json:"path"`
	HealthScore           int            `json:"health_score"`
	GitAvailable          bool           `json:"git_available"`
	Note                  string         `json:"note,omitempty"`
	DeleteCandidates      []Candidate    `json:"delete_candidates"`
	DevArtifactCandidates []Candidate    `json:"dev_artifact_candidates"`
	ArchiveCandidates     []Candidate    `json:"archive_candidates"`
	BrokenLinks           []Candidate    `json:"broken_links"`
	LargeFiles            []Candidate    `json:"large_files"`
	MisplacedScripts      []Candidate    `json:"misplaced_scripts"`
	MisplacedDocs         []Candidate    `json:"misplaced_docs"`
	UntrackCandidates     []Candidate    `json:"untrack_candidates"`
	RenameDocs            []Candidate    `json:"rename_docs,omitempty"`
	AllFiles              []LabeledFile  `json:"all_files,omitempty"`
	Summary               map[string]int `json:"summary"`
}

// Severity labels for completeness items and finding groups.
const (
	sevLabelError   = "error"
	sevLabelWarning = "warning"
	sevLabelInfo    = "info"
)

// MissingItem is one file or pattern a healthy repository should have.
type MissingItem struct {
	Name     string `json:"name"`
	Severity string `json:"severity"` // "error", "warning", "info"
	Why      string `json:"why"`
}

// CompletenessReport is the repository-completeness gap report.
type CompletenessReport struct {
	Schema  string        `json:"schema"`
	Path    string        `json:"path"`
	Missing []MissingItem `json:"missing"`
	Score   int           `json:"score"` // 0-100, 100 = fully complete
}

// FindingEntry is one file-level signal inside a findings group.
type FindingEntry struct {
	Severity string `json:"severity"`
	File     string `json:"file"`
	SizeKB   int64  `json:"size_kb"`
	Score    int    `json:"score"`
	Message  string `json:"message"`
}

// FindingGroup aggregates all entries for one rule.
type FindingGroup struct {
	Rule    string         `json:"rule"`
	Count   int            `json:"count"`
	Entries []FindingEntry `json:"entries"`
}

// FindingsReport groups every emitted signal by rule, sorted by count
// descending then rule name, so the "why" behind a plan is inspectable
// before anything is applied.
type FindingsReport struct {
	Schema           string         `json:"schema"`
	Path             string         `json:"path"`
	GitAvailable     bool           `json:"git_available"`
	FilesWithSignals int            `json:"files_with_signals"`
	Groups           []FindingGroup `json:"groups"`
}

// Analysis is the combined output of one clean pass: the enriched file
// inventory that produced the plan and the plan itself.
type Analysis struct {
	Files []FileInfo
	Plan  Plan
}
