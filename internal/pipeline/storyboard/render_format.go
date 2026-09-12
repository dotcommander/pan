package storyboard

import (
	"fmt"
	"sort"
	"strings"
)

func compactStageLabel(label, snippet string) string {
	label = semanticStageLabel(strings.TrimSpace(label))
	signature := compactFuncSignature(snippet)
	if signature == "" {
		return label
	}
	name := signatureName(signature)
	if name == "" {
		return label
	}
	if label == name {
		return signature
	}
	if rest, ok := strings.CutPrefix(label, name+" - "); ok {
		if generatedSignatureSubtitle(rest) {
			return signature
		}
		return signature + " - " + rest
	}
	if strings.HasPrefix(label, name+" ") {
		return signature + strings.TrimPrefix(label, name)
	}
	return label
}

func generatedSignatureSubtitle(rest string) bool {
	return strings.Contains(rest, "→") ||
		strings.Contains(rest, " · ") ||
		strings.Contains(rest, " params ")
}

func semanticStageLabel(label string) string {
	switch label {
	case "fork: fn := call.Fun.(type)":
		return "fork: resolve call target"
	case "*ast.Ident -> return fn.Name":
		return "direct identifier -> return fn.Name"
	case "*ast.SelectorExpr -> return fn.Sel.Name":
		return "selector call -> return fn.Sel.Name"
	case "fork: e := expr.(type)":
		return "fork: resolve expression name"
	case "*ast.Ident -> return e.Name":
		return "identifier expression -> return e.Name"
	case "*ast.SelectorExpr -> return e.Sel.Name":
		return "selector expression -> return e.Sel.Name"
	case "fork: len(parts)":
		return "fork: stem segment count"
	case "1 -> return \"\"":
		return "single part -> return \"\""
	case "2 -> return parts[0]":
		return "two parts -> return parts[0]"
	case "default -> return parts[0] + \"/\" + parts[1]":
		return "default -> return parts[0] + \"/\" + parts[1]"
	default:
		return label
	}
}

func compactFuncSignature(snippet string) string {
	snippet = strings.TrimSpace(snippet)
	if !strings.HasPrefix(snippet, "func ") {
		return ""
	}
	signature := strings.TrimPrefix(snippet, "func ")
	signature = strings.TrimSpace(strings.TrimSuffix(signature, "{"))
	return strings.TrimSpace(signature)
}

func signatureName(signature string) string {
	if idx := strings.IndexByte(signature, '('); idx > 0 {
		return signature[:idx]
	}
	return ""
}

func trimDisplayPath(path string) string {
	return strings.TrimPrefix(path, "internal/")
}

func truncateDisplay(text string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	if maxLen == 1 {
		return "…"
	}
	return string(runes[:maxLen-1]) + "…"
}

func formatFileListWithRoles(files []string) string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, formatFileWithRole(file))
	}
	return strings.Join(out, ", ")
}

func formatFileWithRole(file string) string {
	label := trimDisplayPath(file)
	if role := fileRole(file); role != "" {
		label += " [" + role + "]"
	}
	return label
}

func formatFileSummary(files []string) string {
	if len(files) == 1 {
		return formatFileListWithRoles(files)
	}

	counts := map[string]int{}
	for _, file := range files {
		role := fileRole(file)
		if role == "" {
			role = "other"
		}
		counts[role]++
	}

	order := fileRoleOrder()
	known := make(map[string]bool, len(order))
	parts := make([]string, 0, len(counts))
	for _, role := range order {
		known[role] = true
		if role == "other" {
			continue // printed last, after derived package roles
		}
		if count := counts[role]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s x%d", role, count))
		}
	}
	// Derived package roles (foreign repos) are not in the curated order;
	// print them alphabetically after curated roles, before "other".
	derived := make([]string, 0, len(counts))
	for role := range counts {
		if !known[role] {
			derived = append(derived, role)
		}
	}
	sort.Strings(derived)
	for _, role := range derived {
		parts = append(parts, fmt.Sprintf("%s x%d", role, counts[role]))
	}
	if count := counts[roleOther]; count > 0 {
		parts = append(parts, fmt.Sprintf("%s x%d", roleOther, count))
	}
	return "files: " + strings.Join(parts, ", ")
}

func fileRoleOrder() []string {
	return []string{
		"entrypoint", "command registry", "command surface", "routing detector", "AST detector", "stage extractor", "phase classifier", "spec merge", "source signal table", "doc enrichment", "HTML renderer", "live server", "storyboard renderer", "audit packet", "spec contract", "starter data", "build metadata", roleOther,
	}
}

func fileRole(path string) string {
	switch {
	case strings.Contains(path, "/commands/root.go") || strings.HasSuffix(path, "commands/root.go"):
		return "command registry"
	case strings.Contains(path, "/commands/"):
		return "command surface"
	case strings.HasPrefix(path, "cmd/"):
		return "entrypoint"
	}
	for _, rule := range fileRoleRules() {
		if strings.Contains(path, rule.path) {
			return rule.role
		}
	}
	return packageRole(path)
}

type fileRoleRule struct{ path, role string }

func fileRoleRules() []fileRoleRule {
	return []fileRoleRule{
		{"/scan/dispatcher.go", "routing detector"}, {"/scan/detect.go", "AST detector"}, {"/scan/fill.go", "stage extractor"}, {"/scan/mapper.go", "phase classifier"}, {"/scan/merge.go", "spec merge"}, {"/scan/source_signals.go", "source signal table"}, {"/scan/subtitle.go", "doc enrichment"}, {"/render/", "HTML renderer"}, {"/serve/", "live server"}, {"/storyboard/", "storyboard renderer"}, {"/auditpacket/", "audit packet"}, {"/spec/", "spec contract"}, {"/seed/", "starter data"}, {"/buildinfo/", "build metadata"},
	}
}

// packageRole derives a display role from a file's immediate package directory
// when no curated role matches, so foreign repos get architectural labels
// (e.g. "notifier", "search", "database") instead of a flat "other" bucket.
// Generic container segments (internal, pkg, cmd, …) are skipped so the label
// is the meaningful package name. Returns "" for a top-level file with no
// package directory.
func packageRole(path string) string {
	path = strings.TrimSuffix(path, "/")
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return ""
	}
	segs := strings.Split(path[:idx], "/")
	for i := len(segs) - 1; i >= 0; i-- {
		switch segs[i] {
		case "", "internal", "pkg", "cmd", "src", "app", "lib":
			continue
		}
		return segs[i]
	}
	return ""
}
