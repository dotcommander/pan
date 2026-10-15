package ranking

import (
	"path"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
)

// applyImportScores resolves each Go file's package directory to a module
// import key, counts unique importers per key, and distributes the import
// component. Directories are also wired into InternalImports/DependsOn for
// map and task rendering.
func applyImportScores(ranked []RankedFile, modulePath string) {
	keys := importKeys(ranked, modulePath)
	internalDirs := internalDirSet(ranked)
	importers := countImporters(ranked, modulePath, internalDirs)
	distributeImportScores(ranked, keys, importers)
}

// dirImportKey maps a repo-relative file path to its package directory,
// with "" for repository-root files.
func dirImportKey(p string) string {
	if dir := path.Dir(p); dir != "." {
		return dir
	}
	return ""
}

// fullImportKey maps a package directory to its module-qualified import key.
func fullImportKey(dir, modulePath string) string {
	if dir == "" || dir == "." {
		return modulePath
	}
	if modulePath == "" {
		return dir
	}
	return modulePath + "/" + dir
}

// importKeys maps package import keys to the ranked files that declare them.
// Keys are module-qualified when known and repository-relative otherwise.
func importKeys(ranked []RankedFile, modulePath string) map[string][]int {
	keys := make(map[string][]int, len(ranked))
	for i := range ranked {
		if ranked[i].Language != analyze.LanguageGo {
			continue
		}
		key := fullImportKey(dirImportKey(ranked[i].Path), modulePath)
		keys[key] = append(keys[key], i)
	}
	return keys
}

// internalDirSet uses the same repository-relative namespace as resolved imports.
// Module-qualified score keys must not be used for this membership check.
func internalDirSet(ranked []RankedFile) map[string]struct{} {
	dirs := make(map[string]struct{}, len(ranked))
	for _, file := range ranked {
		if file.Language == analyze.LanguageGo {
			dirs[path.Dir(file.Path)] = struct{}{}
		}
	}
	return dirs
}

// countImporters wires each Go file's internal imports into InternalImports
// and DependsOn, and returns the unique importer file paths per import key.
func countImporters(ranked []RankedFile, modulePath string, internalDirs map[string]struct{}) map[string]map[string]struct{} {
	importers := make(map[string]map[string]struct{}) // key -> importing file paths
	for i := range ranked {
		rf := &ranked[i]
		if rf.Language != analyze.LanguageGo {
			continue
		}
		seen := make(map[string]struct{})
		for _, imp := range rf.Imports {
			dir, ok := resolveImportDir(imp, modulePath, internalDirs)
			if !ok {
				continue
			}
			if _, dup := seen[dir]; dup {
				continue
			}
			seen[dir] = struct{}{}
			rf.InternalImports = append(rf.InternalImports, dir)
			key := dir
			if modulePath != "" {
				key = fullImportKey(dir, modulePath)
			}
			if importers[key] == nil {
				importers[key] = make(map[string]struct{})
			}
			importers[key][rf.Path] = struct{}{}
		}
		slices.Sort(rf.InternalImports)
		rf.DependsOn = len(rf.InternalImports)
	}
	return importers
}

// resolveImportDir maps one import path to the internal directory it
// references: a module-prefixed import resolves by trimming the prefix,
// otherwise an exact suffix match against known internal directories is
// accepted. ok is false when the import is not internal to the module.
func resolveImportDir(imp, modulePath string, internalDirs map[string]struct{}) (string, bool) {
	var dir string
	switch {
	case modulePath != "" && strings.HasPrefix(imp, modulePath+"/"):
		dir = strings.TrimPrefix(imp, modulePath+"/")
	case modulePath != "" && imp == modulePath:
		dir = "."
	case modulePath == "":
		// No module prefix: accept an exact suffix match against known
		// internal directories (basename-style resolution).
		if !resolveSuffix(imp, internalDirs, &dir) {
			return "", false
		}
	default:
		return "", false
	}
	if _, known := internalDirs[dir]; !known {
		return "", false
	}
	if dir == "" {
		dir = "."
	}
	return dir, true
}

// distributeImportScores assigns the import component and ImportedBy count
// to every file declaring an imported key.
func distributeImportScores(ranked []RankedFile, keys map[string][]int, importers map[string]map[string]struct{}) {
	for key, sources := range importers {
		count := len(sources)
		for _, i := range keys[key] {
			addComponent(&ranked[i], ComponentImports, importScore(count))
			ranked[i].ImportedBy = count
		}
	}
}

// resolveSuffix finds the internal directory whose path is the suffix of imp.
func resolveSuffix(imp string, dirs map[string]struct{}, out *string) bool {
	best := ""
	for dir := range dirs {
		if dir != "." && (imp == dir || strings.HasSuffix(imp, "/"+dir)) &&
			(len(dir) > len(best) || (len(dir) == len(best) && dir < best)) {
			best = dir
		}
	}
	if best == "" {
		return false
	}
	*out = best
	return true
}
