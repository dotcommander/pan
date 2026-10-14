package clean

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/dotcommander/pan/internal/config"
)

func isDeleteCandidate(f *FileInfo, deleteNames, deleteExts, deleteDirs map[string]bool) (bool, string) {
	name := filepath.Base(f.RelPath)
	if deleteNames[name] {
		return true, "system file"
	}
	ext := filepath.Ext(name)
	if deleteExts[ext] || strings.HasSuffix(name, "~") {
		return true, "temp/backup file"
	}
	if f.IsDir && deleteDirs[name] {
		return true, "cache directory"
	}
	if f.HasFinding(RuleEmpty) {
		return true, "empty directory"
	}
	return false, ""
}

func isDevArtifact(f *FileInfo, rules config.CleanRules) bool {
	if !f.Tracked || f.IsDir {
		return false
	}
	// Dev artifacts are loose scratch files near the root, not nested
	// reference docs.
	if strings.Count(filepath.ToSlash(f.RelPath), "/") > 1 {
		return false
	}
	name := filepath.Base(f.RelPath)
	for _, p := range rules.DevArtifactPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	for _, s := range rules.DevArtifactSuffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

func isMisplacedDoc(f *FileInfo, allowedMD map[string]bool) bool {
	if f.IsDir || !f.Tracked {
		return false
	}
	name := filepath.Base(f.RelPath)
	rel := filepath.ToSlash(f.RelPath)
	return strings.HasSuffix(name, ".md") && !strings.Contains(rel, "/") && !allowedMD[name]
}

// untrackPolicy bundles the untrack pattern tables so disposition
// checks stay within the argument limit.
type untrackPolicy struct {
	exts   map[string]bool
	images map[string]bool
	dirs   []string
}

// isUntrackCandidate reports whether a tracked file should leave the git
// index, with the reason.
func isUntrackCandidate(f *FileInfo, policy untrackPolicy) (bool, string) {
	if !f.Tracked || f.IsDir || f.IsSymlink {
		return false, ""
	}
	name := filepath.Base(f.RelPath)
	rel := filepath.ToSlash(f.RelPath)
	ext := filepath.Ext(name)
	return untrackDisposition(f, name, rel, ext, policy)
}

func untrackDisposition(f *FileInfo, name, rel, ext string, policy untrackPolicy) (bool, string) {
	if policy.exts[ext] {
		return true, "binary/archive extension"
	}
	if policy.images[ext] && !strings.Contains(rel, "/") {
		return true, "image file at repo root"
	}
	if isEnvFile(name) {
		return true, "environment file"
	}
	if underAnyPrefix(rel, policy.dirs) {
		return true, "build output directory"
	}
	if isCompiledBinary(f, rel, ext) {
		return true, "compiled binary"
	}
	if f.HasFinding(RuleGenerated) {
		return true, "generated content"
	}
	return false, ""
}

// isEnvFile matches .env and non-sample .env.* files.
func isEnvFile(name string) bool {
	if name == ".env" {
		return true
	}
	return strings.HasPrefix(name, ".env.") &&
		!strings.HasSuffix(name, ".example") && !strings.HasSuffix(name, ".sample") && !strings.HasSuffix(name, ".template")
}

func underAnyPrefix(rel string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

// isCompiledBinary matches extensionless executables outside scripts/ and
// hooks/.
func isCompiledBinary(f *FileInfo, rel, ext string) bool {
	return f.Executable && ext == "" && !strings.HasPrefix(rel, "scripts/") && !strings.HasPrefix(rel, "hooks/")
}

// normalizeDocName returns the kebab-case lowercase form of a filename, or
// "" when the name is already normalized.
func normalizeDocName(name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	normalized := strings.ToLower(strings.ReplaceAll(base, "_", "-")) + strings.ToLower(ext)
	if normalized == name {
		return ""
	}
	return normalized
}

func needsDocRename(f *FileInfo) (string, bool) {
	if f.IsDir {
		return "", false
	}
	rel := filepath.ToSlash(f.RelPath)
	if !strings.HasPrefix(rel, "docs/") {
		return "", false
	}
	name := filepath.Base(rel)
	if strings.EqualFold(name, "readme.md") || strings.HasSuffix(name, "_test.go") {
		return "", false
	}
	if norm := normalizeDocName(name); norm != "" {
		return path.Join(path.Dir(rel), norm), true
	}
	return "", false
}

func isMisplacedScript(f *FileInfo) bool {
	if f.IsDir || f.IsSymlink {
		return false
	}
	rel := filepath.ToSlash(f.RelPath)
	return strings.HasSuffix(rel, ".sh") && !strings.Contains(rel, "/")
}
