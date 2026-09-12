package scan

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Bounds on the change-history window inspected.
const (
	maxChangeCommits  = 500
	maxRecentCommits  = 20
	defaultChangeDays = 30
	commitFieldSep    = '\x1f'
)

// changeLogWalkBound is the literal git --max-count bound for the change
// log walk. It is a constant so the git argument vector carries no dynamic
// value; it must stay in sync with maxChangeCommits, and a unit test
// asserts the pairing.
const changeLogWalkBound = "--max-count=500"

// changeLogFormat is the literal git pretty format for the change log:
// hash, committer date (ISO 8601), and subject, separated by
// commitFieldSep. A constant keeps the argument vector literal; the parser
// splits records on the same separator.
const changeLogFormat = "--pretty=format:%H\x1f%cI\x1f%s"

// FileChurn records how often one path changed inside the window.
type FileChurn struct {
	Path       string `json:"path"`
	Commits    int    `json:"commits"`
	FixTouches int    `json:"fix_touches"`
}

// Commit summarizes one commit inside the window.
type Commit struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
	Files   int    `json:"files"`
}

// ChangesReport is the deterministic change-evidence packet derived from
// bounded git history. When git or history is unavailable the report
// degrades to an explicit note instead of failing.
type ChangesReport struct {
	GitAvailable bool        `json:"git_available"`
	Days         int         `json:"days"`
	Commits      int         `json:"commits"`
	FixCommits   int         `json:"fix_commits"`
	FilesTouched int         `json:"files_touched"`
	TopFiles     []FileChurn `json:"top_files"`
	Recent       []Commit    `json:"recent,omitempty"`
	Note         string      `json:"note,omitempty"`
}

var (
	fixSubjectPattern = regexp.MustCompile(`(?i)\bfix(?:es|ed)?\b`)
	hashPattern       = regexp.MustCompile(`^[0-9a-f]{40}(?:[0-9a-f]{24})?$`)
)

// Changes summarizes per-file churn over a bounded history window ending
// at asOf. days <= 0 → defaultChangeDays; a zero asOf means the whole
// bounded history (still capped at maxChangeCommits). The window keeps
// commits whose committer date is at least as recent as the cutoff, the
// same date git's --since filter uses, applied in Go so the git argument
// vector stays fully literal. top > 0 caps the churn list; recent commits
// are always capped at maxRecentCommits. Given the same repository state,
// asOf, and days, the result is deterministic.
func Changes(ctx context.Context, root string, days, top int, asOf time.Time) ChangesReport {
	if days <= 0 {
		days = defaultChangeDays
	}
	cutoff := asOf.AddDate(0, 0, -days).UTC()
	out, err := gitChangeLog(ctx, root)
	if err != nil {
		return ChangesReport{
			GitAvailable: false,
			Days:         days,
			Note:         fmt.Sprintf("git history unavailable (%s); change evidence not assessed", causeErrorText(err)),
		}
	}
	commits := commitsWithinWindow(parseChangeLog(out), cutoff)

	report := ChangesReport{GitAvailable: true, Days: days}
	churn := map[string]*FileChurn{}
	for _, commit := range commits {
		report.Commits++
		fix := fixSubjectPattern.MatchString(commit.subject)
		if fix {
			report.FixCommits++
		}
		report.Recent = appendBounded(report.Recent, Commit{Hash: commit.hash, Subject: commit.subject, Files: len(commit.files)}, maxRecentCommits)
		for _, file := range commit.files {
			entry, ok := churn[file]
			if !ok {
				entry = &FileChurn{Path: file}
				churn[file] = entry
			}
			entry.Commits++
			if fix {
				entry.FixTouches++
			}
		}
	}
	report.FilesTouched = len(churn)
	report.TopFiles = churnList(churn)
	if top > 0 && len(report.TopFiles) > top {
		report.TopFiles = report.TopFiles[:top]
	}
	return report
}

// changeCommit is one parsed log record.
type changeCommit struct {
	hash    string
	when    time.Time
	subject string
	files   []string
}

// gitChangeLog runs one bounded git log invocation with a fully literal
// argument vector: the working directory carries the repository root and
// the walk bound and pretty format are constants, so no dynamic value ever
// reaches the subprocess. The window itself is filtered in Go from each
// commit's committer date.
func gitChangeLog(ctx context.Context, root string) (string, error) {
	return readGitBounded(
		ctx, root, maxGitOutputBytes,
		"-c", "core.quotepath=off",
		"log",
		changeLogWalkBound,
		"--name-only",
		"--no-renames",
		changeLogFormat,
	)
}

// parseChangeLog splits the git log stream into commit records. Each commit
// header is "<hash>\x1f<committer date>\x1f<subject>"; file paths follow on
// their own lines until the next header or blank separator line. A line
// only counts as a header when it starts with a full commit hash, so
// subjects and paths never collide with the grammar.
func parseChangeLog(out string) []changeCommit {
	var commits []changeCommit
	current := -1
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			current = -1
			continue
		}
		if hash, rest, found := strings.Cut(line, string(commitFieldSep)); found && hashPattern.MatchString(hash) {
			date, subject, _ := strings.Cut(rest, string(commitFieldSep))
			commits = append(commits, changeCommit{hash: hash, when: commitTime(date), subject: subject})
			current = len(commits) - 1
			continue
		}
		if current >= 0 {
			commits[current].files = append(commits[current].files, line)
		}
	}
	return commits
}

// commitTime parses one ISO 8601 committer date; an unparseable date maps
// to the zero time, which commitsWithinWindow keeps rather than dropping
// evidence on a format surprise.
func commitTime(value string) time.Time {
	when, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return when
}

// commitsWithinWindow keeps commits whose committer date is at least as
// recent as the cutoff (the same date git's --since filter uses) and caps
// the result at maxChangeCommits, mirroring git's output cap after
// revision limiting. Commits without a parseable date are kept.
func commitsWithinWindow(commits []changeCommit, cutoff time.Time) []changeCommit {
	out := make([]changeCommit, 0, len(commits))
	for _, commit := range commits {
		if commit.when.IsZero() || !commit.when.Before(cutoff) {
			out = append(out, commit)
		}
	}
	if len(out) > maxChangeCommits {
		out = out[:maxChangeCommits]
	}
	return out
}

func churnList(churn map[string]*FileChurn) []FileChurn {
	out := make([]FileChurn, 0, len(churn))
	for _, entry := range churn {
		out = append(out, *entry)
	}
	slices.SortFunc(out, func(a, b FileChurn) int {
		if a.Commits != b.Commits {
			return b.Commits - a.Commits
		}
		if a.FixTouches != b.FixTouches {
			return b.FixTouches - a.FixTouches
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out
}

func appendBounded[T any](items []T, item T, bound int) []T {
	if bound > 0 && len(items) >= bound {
		return items
	}
	return append(items, item)
}
