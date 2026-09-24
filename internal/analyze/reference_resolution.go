package analyze

import (
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const (
	maxUnresolvedReferences = 64
	maxReferenceCandidates  = 5
)

type sourceReferenceKey struct {
	Path string
	Name string
	Line int
}

func (b *builder) resolveReferences() {
	definitions := make(map[string][]Symbol)
	declarations := make(map[sourceReferenceKey]bool)
	files := make(map[string]bool, len(b.snap.Files))
	for _, file := range b.snap.Files {
		files[file.Path] = true
	}
	for _, symbol := range b.snap.Symbols {
		definitions[symbol.Name] = append(definitions[symbol.Name], symbol)
		declarations[sourceReferenceKey{symbol.Location.Path, symbol.Name, symbol.Location.Line}] = true
	}
	fromPaths := make([]string, 0, len(b.references))
	for from := range b.references {
		fromPaths = append(fromPaths, from)
	}
	slices.Sort(fromPaths)
	for _, from := range fromPaths {
		for _, reference := range b.references[from] {
			if declarations[sourceReferenceKey{from, reference.Name, reference.Line}] {
				continue
			}
			b.resolveReference(from, reference, definitions, files)
		}
	}
}

func (b *builder) resolveReference(from string, reference sourceReference, definitions map[string][]Symbol, files map[string]bool) {
	var imports []sourceBinding
	for _, binding := range b.bindings[from] {
		if binding.Local == reference.Name {
			imports = append(imports, binding)
		}
	}
	if len(imports) == 0 {
		var paths []string
		for _, symbol := range definitions[reference.Name] {
			if symbol.Location.Path != from && referenceLanguageCompatible(from, symbol.Location.Path) {
				paths = append(paths, symbol.Location.Path)
			}
		}
		if len(paths) == 0 {
			return // Ordinary local identifier; no cross-file claim.
		}
		b.unresolvedReference(from, reference, "no_import_binding", paths)
		return
	}
	if b.shadows[from][reference.Name] {
		b.unresolvedReference(from, reference, "shadowed", nil)
		return
	}
	if len(imports) != 1 {
		b.unresolvedReference(from, reference, "ambiguous_binding", nil)
		return
	}
	var targets []Symbol
	var candidates []string
	for _, binding := range imports {
		for _, candidate := range modulePaths(from, binding.Module) {
			candidates = append(candidates, candidate)
			if !files[candidate] {
				continue
			}
			for _, symbol := range definitions[binding.Symbol] {
				if symbol.Location.Path == candidate {
					targets = append(targets, symbol)
				}
			}
		}
	}
	if len(targets) != 1 {
		reason := "unresolved_target"
		if len(targets) > 1 {
			reason = "ambiguous_binding"
		}
		b.unresolvedReference(from, reference, reason, candidates)
		return
	}
	target := targets[0]
	if target.Location.Path == from {
		return
	}
	b.addEdge(Edge{From: from, To: target.Location.Path, Kind: "references", Symbol: target.Name, Confidence: ConfidenceSyntactic, Location: Location{Path: from, Line: reference.Line, Column: reference.Column}})
}

func referenceLanguageCompatible(from, candidate string) bool {
	source, target := LanguageForPath(from), LanguageForPath(candidate)
	if source == LanguagePython {
		return target == LanguagePython
	}
	switch source {
	case LanguageJavascript, LanguageJsx, LanguageTypescript, LanguageTsx:
		switch target {
		case LanguageJavascript, LanguageJsx, LanguageTypescript, LanguageTsx:
			return true
		}
	}
	return false
}

// modulePaths admits only explicitly spelled relative JS modules or Python
// modules corresponding to a captured path. External packages are not guessed.
func modulePaths(from, module string) []string {
	language := LanguageForPath(from)
	var base string
	switch language {
	case LanguagePython:
		if module == "" {
			return nil
		}
		dots := len(module) - len(strings.TrimLeft(module, "."))
		name := strings.ReplaceAll(module[dots:], ".", "/")
		base = name
		if dots > 0 {
			base = path.Dir(from)
			for i := 1; i < dots; i++ {
				base = path.Dir(base)
			}
			base = path.Join(base, name)
		}
	case LanguageJavascript, LanguageJsx, LanguageTypescript, LanguageTsx:
		if !strings.HasPrefix(module, "./") && !strings.HasPrefix(module, "../") {
			return nil
		}
		base = path.Join(path.Dir(from), module)
	default:
		return nil
	}
	if base == "" || !filepath.IsLocal(filepath.FromSlash(base)) {
		return nil
	}
	if language == LanguagePython {
		return []string{base + ".py", path.Join(base, "__init__.py")}
	}
	if path.Ext(base) != "" {
		return []string{base}
	}
	var paths []string
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx"} {
		paths = append(paths, base+ext, path.Join(base, "index"+ext))
	}
	return paths
}

func (b *builder) unresolvedReference(from string, reference sourceReference, reason string, paths []string) {
	b.snap.UnresolvedCount++
	paths = sortedUnique(paths)
	record := UnresolvedReference{From: from, Name: reference.Name, Line: reference.Line, Column: reference.Column, Reason: reason, CandidateCount: len(paths)}
	if len(paths) > maxReferenceCandidates {
		paths = paths[:maxReferenceCandidates]
	}
	record.Candidates = paths
	if len(b.snap.UnresolvedReferences) < maxUnresolvedReferences {
		b.snap.UnresolvedReferences = append(b.snap.UnresolvedReferences, record)
		return
	}
	b.snap.UnresolvedTruncation = &Truncation{Field: "unresolved_references", Shown: maxUnresolvedReferences, Total: b.snap.UnresolvedCount, Reason: "unresolved evidence cap"}
}
