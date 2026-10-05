// Package bench scores Pan's retrieval against issue-to-file ground truth
// datasets. It reads only local inputs: a SWE-bench-style JSONL dataset and a
// directory of local git mirrors. It never downloads data, never contacts the
// network, and never executes repository code; checkouts come from read-only
// `git archive` extractions under a bounded work directory.
package bench

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Bench bounds. One dataset line may carry a full patch, so the line cap is
// generous; the request cap keeps oversized problem statements bounded with
// explicit evidence.
const (
	maxInstanceLineBytes = 8 << 20
	maxRequestBytes      = 64 << 10
)

// Instance is one SWE-bench-style dataset record: the issue text, the
// repository and commit the issue applies to, and the gold patch whose
// touched files are the retrieval target.
type Instance struct {
	ID               string `json:"instance_id"`
	Repo             string `json:"repo"`
	BaseCommit       string `json:"base_commit"`
	ProblemStatement string `json:"problem_statement"`
	Patch            string `json:"patch"`
}

// validate enforces the dataset contract for one instance.
func (i Instance) validate() error {
	switch {
	case strings.TrimSpace(i.ID) == "":
		return errors.New("instance_id is required")
	case strings.TrimSpace(i.Repo) == "":
		return errors.New("repo is required")
	case strings.TrimSpace(i.BaseCommit) == "":
		return errors.New("base_commit is required")
	case strings.TrimSpace(i.ProblemStatement) == "":
		return errors.New("problem_statement is required")
	case strings.TrimSpace(i.Patch) == "":
		return errors.New("patch is required")
	}
	if strings.ContainsAny(i.ID, `/\`) {
		return errors.New("instance_id must not contain path separators")
	}
	if _, _, ok := splitRepo(i.Repo); !ok {
		return fmt.Errorf("repo %q must be org/name", i.Repo)
	}
	return nil
}

// splitRepo splits one org/name repository identifier.
func splitRepo(repo string) (org, name string, ok bool) {
	org, name, found := strings.Cut(repo, "/")
	if !found || org == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return org, name, true
}

// LoadInstances reads the dataset JSONL, validating every record. filters
// keeps only exact org/name matches when non-empty; limit caps the returned
// instances after filtering (zero keeps all).
func LoadInstances(path string, filters []string, limit int) ([]Instance, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open dataset: %w", err)
	}
	defer func() { _ = file.Close() }()
	filtered := len(filters) > 0
	var out []Instance
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxInstanceLineBytes)
	for line := 1; scanner.Scan(); line++ {
		raw := scanner.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var instance Instance
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&instance); err != nil {
			return nil, fmt.Errorf("dataset line %d: %w", line, err)
		}
		if err := instance.validate(); err != nil {
			return nil, fmt.Errorf("dataset line %d: %w", line, err)
		}
		if filtered && !slices.Contains(filters, instance.Repo) {
			continue
		}
		out = append(out, instance)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}
	if len(out) == 0 {
		return nil, errors.New("dataset has no matching instances")
	}
	return out, nil
}

// GoldPaths extracts the touched-file set from one unified diff patch.
// Added and modified files come from `+++ b/<path>` headers; deleted files
// (whose new side is /dev/null) come from `--- a/<path>`. The result is
// deduplicated and sorted; an empty result means the patch names no files.
func GoldPaths(patch string) []string {
	var paths []string
	seen := make(map[string]struct{})
	lines := strings.Split(patch, "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "+++ "):
			addGoldPath(&paths, seen, diffSidePath(strings.TrimPrefix(line, "+++")))
		case strings.HasPrefix(line, "--- a/"):
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ /dev/null") {
				addGoldPath(&paths, seen, diffSidePath(strings.TrimPrefix(line, "---")))
			}
		}
	}
	slices.Sort(paths)
	return paths
}

// diffSidePath trims one diff side value to its file path, mapping
// /dev/null to the empty string, stripping an optional a/ or b/ prefix,
// and dropping a trailing tab timestamp from git-format headers.
func diffSidePath(side string) string {
	side = strings.TrimSpace(side)
	if side == "" || side == "/dev/null" {
		return ""
	}
	if tab := strings.IndexByte(side, '\t'); tab >= 0 {
		side = side[:tab]
	}
	side = strings.TrimSpace(side)
	side = strings.TrimPrefix(side, "b/")
	side = strings.TrimPrefix(side, "a/")
	return side
}

func addGoldPath(paths *[]string, seen map[string]struct{}, path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	if _, dup := seen[path]; dup {
		return
	}
	seen[path] = struct{}{}
	*paths = append(*paths, path)
}

// MirrorPath resolves one repository's local mirror under mirrorsDir using
// the dataset convention org/name -> org__name.
func MirrorPath(mirrorsDir, repo string) string {
	org, name, ok := splitRepo(repo)
	if !ok {
		return ""
	}
	return filepath.Join(mirrorsDir, org+"__"+name)
}

// BoundedRequest truncates one problem statement to the request cap on a
// rune boundary, reporting whether truncation happened.
func BoundedRequest(statement string) (string, bool) {
	if len(statement) <= maxRequestBytes {
		return statement, false
	}
	cut := statement[:maxRequestBytes]
	for len(cut) > 0 && !utf8RuneStart(cut[len(cut)-1]) {
		cut = cut[:len(cut)-1]
	}
	return cut, true
}

// utf8RuneStart reports whether b begins a UTF-8 sequence.
func utf8RuneStart(b byte) bool {
	return b&0xC0 != 0x80
}
