package review

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
	"github.com/dotcommander/pan/internal/pipeline/symbols"
)

func rootedPath(root, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	return filepath.Clean(path)
}

// skipDir reports whether a relative path should be excluded from coverage checks.
func skipDir(rel string) bool {
	parts := strings.Split(rel, string(filepath.Separator))
	for _, p := range parts {
		switch p {
		case "testdata", "examples", "vendor", ".git", "out":
			return true
		}
		if strings.HasPrefix(p, ".") && len(p) > 1 {
			return true
		}
	}
	return false
}

// fileRefExists resolves one phase file reference (literal or glob) against
// root and reports whether it matches at least one path on disk.
func fileRefExists(root, ref string) bool {
	abs := rootedPath(root, ref)
	if spec.HasGlobMeta(ref) {
		matches, err := filepath.Glob(abs)
		return err == nil && len(matches) > 0
	}
	_, err := os.Stat(abs)
	return err == nil
}

// missingFileMessage explains why a file reference matched nothing.
func missingFileMessage(ref string) string {
	if spec.HasGlobMeta(ref) {
		return "glob matched no files: " + ref
	}
	return "file not found: " + ref
}

// checkExistence verifies that every file listed in phase.Files exists on disk.
func checkExistence(s *spec.Spec, root string) []Finding {
	var findings []Finding
	for _, p := range s.AllPhases() {
		for _, f := range p.Files {
			if fileRefExists(root, f) {
				continue
			}
			findings = append(findings, Finding{
				Check:    checkNameExistence,
				Severity: High,
				Phase:    p.Name,
				Message:  missingFileMessage(f),
			})
		}
	}
	return findings
}

// checkPhaseOrphans flags phases where every referenced file is missing.
func checkPhaseOrphans(s *spec.Spec, root string) []Finding {
	var findings []Finding
	for _, p := range s.AllPhases() {
		if len(p.Files) == 0 || !allFilesMissing(p.Files, root) {
			continue
		}
		findings = append(findings, Finding{
			Check:    checkNamePhaseOrphan,
			Severity: High,
			Phase:    p.Name,
			Message:  "phase " + p.Name + ": all " + strconv.Itoa(len(p.Files)) + " referenced files are missing",
		})
	}
	return findings
}

// allFilesMissing reports whether every file reference matches nothing.
func allFilesMissing(files []string, root string) bool {
	for _, f := range files {
		if fileRefExists(root, f) {
			return false
		}
	}
	return true
}

// checkCoverageGap finds .go files (excluding tests) in the Pan that are
// not referenced by any phase in the spec.
func checkCoverageGap(s *spec.Spec, root string, ranked []symbols.RankedFile) []Finding {
	specFiles := specReferencedFiles(s, root)
	knownMissing := knownMissingSet(s)
	var findings []Finding
	for _, rf := range ranked {
		rankedPath := rootedPath(root, rf.Path)
		rel := rankedGoRel(root, rankedPath, rf)
		if rel == "" || specFiles[rankedPath] || knownMissing[filepath.Clean(rel)] {
			continue
		}
		findings = append(findings, Finding{
			Check:    checkNameCoverageGap,
			Severity: Medium,
			Message:  "file " + rel + " not referenced by any phase",
		})
	}
	return findings
}

// specReferencedFiles expands every phase file reference (literal and glob)
// into the set of absolute paths the spec claims.
func specReferencedFiles(s *spec.Spec, root string) map[string]bool {
	specFiles := make(map[string]bool)
	for _, p := range s.AllPhases() {
		for _, f := range p.Files {
			if spec.HasGlobMeta(f) {
				matches, err := filepath.Glob(rootedPath(root, f))
				if err == nil {
					for _, m := range matches {
						specFiles[filepath.Clean(m)] = true
					}
				}
				continue
			}
			specFiles[rootedPath(root, f)] = true
		}
	}
	return specFiles
}

// knownMissingSet indexes the scan-declared missing files that the coverage
// gap check must not repeat as drift.
func knownMissingSet(s *spec.Spec) map[string]bool {
	if s.Coverage == nil {
		return nil
	}
	set := make(map[string]bool, len(s.Coverage.Missing))
	for _, path := range s.Coverage.Missing {
		set[filepath.Clean(path)] = true
	}
	return set
}

// rankedGoRel returns the root-relative path of a ranked Go source file
// (test files and skipped directories excluded), or "" when the file is not
// a reviewable Go source.
func rankedGoRel(root, rankedPath string, rf symbols.RankedFile) string {
	if rf.FileSymbols == nil {
		return ""
	}
	if rf.Language != "" && rf.Language != "go" {
		return ""
	}
	if strings.HasSuffix(rf.Path, "_test.go") {
		return ""
	}
	rel, err := filepath.Rel(root, rankedPath)
	if err != nil {
		rel = rf.Path
	}
	if skipDir(rel) {
		return ""
	}
	return rel
}
