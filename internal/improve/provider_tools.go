package improve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/codemap"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/lsp"
	"github.com/dotcommander/pan/internal/provider"
	"github.com/dotcommander/pan/internal/ranking"
	"github.com/dotcommander/pan/internal/retrieval"
)

const (
	providerFunctionTool  = "function"
	providerReadFile      = "read_file"
	providerListDir       = "list_dir"
	providerSearchFiles   = "search_files"
	providerMultiRead     = "multi_read"
	providerFindFiles     = "find_files"
	providerStatFile      = "stat_file"
	providerLSPQuery      = "lsp_query"
	providerDetectProject = "detect_project"
	providerRepoContext   = "repo_context"
	providerFilesKey      = "files"
	providerPatternKey    = "pattern"
	providerIncludeKey    = "include"
	providerActionKey     = "action"
	providerDiagnostics   = "diagnostics"
	providerAudit         = "audit"
	providerBrief         = "brief"
	providerItemsKey      = "items"
	providerSymbolKey     = "symbol"
	jsonArray             = "array"
	jsonObject            = "object"
	jsonString            = "string"
	jsonInteger           = "integer"
	proposalRationale     = "rationale"
	proposalFilePath      = "file_path"
	proposalSymbols       = "symbols"
	providerPathKey       = "path"
	providerProperties    = "properties"
	providerRequired      = "required"
)

func providerExplorationTools() []provider.Tool {
	return []provider.Tool{
		functionTool(providerReadFile, "Read a repo-relative source file, optionally by one-based line range.", map[string]any{kindType: jsonObject, providerProperties: pathRangeSchema(), providerRequired: []string{providerPathKey}}),
		functionTool(providerMultiRead, "Read up to 20 repo-relative source files, optionally by line range.", map[string]any{kindType: jsonObject, providerProperties: map[string]any{providerFilesKey: map[string]any{kindType: jsonArray, providerItemsKey: map[string]any{kindType: jsonObject, providerProperties: pathRangeSchema(), providerRequired: []string{providerPathKey}}}}, providerRequired: []string{providerFilesKey}}),
		functionTool(providerListDir, "List repo-relative files under a directory, optionally to a depth.", map[string]any{kindType: jsonObject, providerProperties: map[string]any{providerPathKey: map[string]any{kindType: jsonString}, "depth": map[string]any{kindType: jsonInteger}}}),
		functionTool(providerSearchFiles, "Search source files for a regular expression.", map[string]any{kindType: jsonObject, providerProperties: map[string]any{providerPatternKey: map[string]any{kindType: jsonString}, providerPathKey: map[string]any{kindType: jsonString}, providerIncludeKey: map[string]any{kindType: jsonString}}, providerRequired: []string{providerPatternKey}}),
		functionTool(providerFindFiles, "Find source files by a glob pattern.", map[string]any{kindType: jsonObject, providerProperties: map[string]any{providerPatternKey: map[string]any{kindType: jsonString}, providerPathKey: map[string]any{kindType: jsonString}}, providerRequired: []string{providerPatternKey}}),
		functionTool(providerStatFile, "Report bounded metadata for one repo-relative file.", map[string]any{kindType: jsonObject, providerProperties: map[string]any{providerPathKey: map[string]any{kindType: jsonString}}, providerRequired: []string{providerPathKey}}),
		functionTool(providerLSPQuery, "Run a bounded read-only language-server query.", map[string]any{kindType: jsonObject, providerProperties: map[string]any{providerActionKey: map[string]any{kindType: jsonString, "enum": []string{"definition", "references", "hover", proposalSymbols, providerDiagnostics}}, providerPathKey: map[string]any{kindType: jsonString}, "line": map[string]any{kindType: jsonInteger}, "character": map[string]any{kindType: jsonInteger}, providerSymbolKey: map[string]any{kindType: jsonString}}, providerRequired: []string{providerActionKey, providerPathKey}}),
		functionTool(providerDetectProject, "Detect project markers and languages below a repo-relative directory.", map[string]any{kindType: jsonObject, providerProperties: map[string]any{providerPathKey: map[string]any{kindType: jsonString}}}),
		functionTool(providerRepoContext, "Build bounded deterministic repository context: audit, map, impact, symbol, or brief.", repoContextSchema()),
	}
}

func pathRangeSchema() map[string]any {
	return map[string]any{providerPathKey: map[string]any{kindType: jsonString}, "start_line": map[string]any{kindType: jsonInteger}, "end_line": map[string]any{kindType: jsonInteger}}
}

func repoContextSchema() map[string]any {
	return map[string]any{"type": jsonObject, providerProperties: map[string]any{providerActionKey: map[string]any{kindType: jsonString, "enum": []string{providerAudit, "map", "impact", providerSymbolKey, providerBrief}}, providerPathKey: map[string]any{kindType: jsonString}, providerSymbolKey: map[string]any{kindType: jsonString}, "intent": map[string]any{kindType: jsonString}, "limit": map[string]any{kindType: jsonInteger}, "tokens": map[string]any{kindType: jsonInteger}, "max_bytes": map[string]any{kindType: jsonInteger}}, providerRequired: []string{providerActionKey}}
}

func providerTools(symbols bool) []provider.Tool {
	tools := providerExplorationTools()
	tools = append(tools, functionTool(providerNoCandidate, "Submit a scoped final result when no justified change was found.", noCandidateToolSchema()))
	if symbols {
		return append(tools, functionTool("submit_deletions", "Submit final top-level symbol deletions exactly once.", deletionToolSchema()))
	}
	return append(tools, functionTool("submit_proposal", "Submit final whole-file replacements exactly once.", proposalToolSchema()))
}

func noCandidateToolSchema() map[string]any {
	return map[string]any{kindType: jsonObject, providerProperties: map[string]any{
		proposalRationale: map[string]any{kindType: jsonString},
		"checked":         map[string]any{kindType: jsonArray, providerItemsKey: map[string]any{kindType: jsonString}},
		"limitations":     map[string]any{kindType: jsonArray, providerItemsKey: map[string]any{kindType: jsonString}},
	}, providerRequired: []string{proposalRationale, "checked"}}
}

func functionTool(name, description string, parameters any) provider.Tool {
	return provider.Tool{Type: providerFunctionTool, Function: provider.FunctionDefinition{Name: name, Description: description, Parameters: parameters}}
}

func proposalToolSchema() map[string]any {
	return map[string]any{kindType: jsonObject, providerProperties: map[string]any{
		proposalRationale: map[string]any{kindType: jsonString},
		"changes":         map[string]any{kindType: jsonArray, providerItemsKey: map[string]any{kindType: jsonObject, providerProperties: map[string]any{proposalFilePath: map[string]any{kindType: jsonString}, "new_contents": map[string]any{kindType: jsonString}, "reasoning": map[string]any{kindType: jsonString}}, providerRequired: []string{proposalFilePath, "new_contents"}}},
	}, providerRequired: []string{proposalRationale, "changes"}}
}

func deletionToolSchema() map[string]any {
	return map[string]any{kindType: jsonObject, providerProperties: map[string]any{
		proposalRationale: map[string]any{kindType: jsonString},
		"deletions":       map[string]any{kindType: jsonArray, providerItemsKey: map[string]any{kindType: jsonObject, providerProperties: map[string]any{proposalFilePath: map[string]any{kindType: jsonString}, proposalSymbols: map[string]any{kindType: jsonArray, providerItemsKey: map[string]any{kindType: jsonString}}, "reasoning": map[string]any{kindType: jsonString}}, providerRequired: []string{proposalFilePath, proposalSymbols}}},
	}, providerRequired: []string{proposalRationale, "deletions"}}
}

type providerReader struct {
	root    string
	exclude map[string]bool
	limit   int
	jinnBin string
}

func newProviderReader(root string, exclude []string, limit int) (*providerReader, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("provider repository path is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve provider repository: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		abs = resolved
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("provider repository %q is not a directory", root)
	}
	set := make(map[string]bool, len(exclude))
	for _, path := range exclude {
		set[filepath.ToSlash(filepath.Clean(path))] = true
	}
	return &providerReader{root: abs, exclude: set, limit: limit}, nil
}

func (r *providerReader) path(rel string) (full, slash string, err error) {
	if filepath.IsAbs(rel) {
		return "", "", errors.New("absolute path is not allowed")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." {
		clean = ""
	}
	if clean == parentDir || strings.HasPrefix(clean, parentDir+string(filepath.Separator)) {
		return "", "", errors.New("path escapes repository")
	}
	slash = filepath.ToSlash(clean)
	if deniedProviderPath(slash) || r.exclude[slash] {
		return "", "", fmt.Errorf("path %q is excluded", rel)
	}
	full = filepath.Join(r.root, clean)
	check, err := filepath.Rel(r.root, full)
	if err != nil || check == parentDir || strings.HasPrefix(check, parentDir+string(filepath.Separator)) {
		return "", "", errors.New("path escapes repository")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(full); resolveErr == nil {
		check, err = filepath.Rel(r.root, resolved)
		if err != nil || check == parentDir || strings.HasPrefix(check, parentDir+string(filepath.Separator)) {
			return "", "", errors.New("path escapes repository through symlink")
		}
	}
	return full, slash, nil
}

func deniedProviderPath(path string) bool {
	for _, part := range strings.Split(path, "/") {
		lower := strings.ToLower(part)
		if part == gitDirName || part == "vendor" || part == "node_modules" || part == "testdata" || strings.HasPrefix(part, ".env") || strings.Contains(lower, "credential") || strings.Contains(lower, "secret") {
			return true
		}
	}
	return false
}

func (r *providerReader) files(path string) ([]string, error) {
	full, _, err := r.path(path)
	if err != nil {
		return nil, err
	}
	var files []string
	err = filepath.WalkDir(full, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(r.root, current)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel != "." && deniedProviderPath(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || r.exclude[rel] {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	sort.Strings(files)
	return files, err
}

func (r *providerReader) bundle(limit int) (string, error) {
	files, err := r.files("")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".go") && rel != "go.mod" {
			continue
		}
		full, _, _ := r.path(rel)
		data, truncated, err := readProviderFile(full)
		if err != nil {
			return "", err
		}
		if truncated {
			return b.String() + "\n[repository source truncated]\n", nil
		}
		section := "\n=== SOURCE: " + rel + " ===\n" + string(data)
		if b.Len()+len(section) > limit {
			b.WriteString("\n[repository source truncated]\n")
			break
		}
		b.WriteString(section)
	}
	return b.String(), nil
}

func (r *providerReader) call(ctx context.Context, name, raw string) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", name, err)
	}
	if !isProviderExplorationTool(name) {
		return "", fmt.Errorf("unsupported read-only tool %q", name)
	}
	if r.jinnBin != "" && name != providerRepoContext && len(r.exclude) == 0 {
		result, err := r.jinnCall(ctx, name, args)
		return r.cap(result), err
	}
	if name == providerLSPQuery && stringArg(args, providerActionKey) == providerDiagnostics {
		if bin, err := exec.LookPath("jinn"); err == nil && len(r.exclude) == 0 {
			external := *r
			external.jinnBin = bin
			result, err := external.jinnCall(ctx, name, args)
			return r.cap(result), err
		}
	}
	return r.callNative(ctx, name, args)
}

func (r *providerReader) callNative(ctx context.Context, name string, args map[string]any) (string, error) {
	path := stringArg(args, providerPathKey)
	switch name {
	case providerReadFile:
		return r.readFileRange(path, intArg(args, "start_line", 0), intArg(args, "end_line", 0))
	case providerMultiRead:
		return r.multiRead(args)
	case providerListDir:
		return r.listDirDepth(path, intArg(args, "depth", 0))
	case providerSearchFiles:
		return r.searchFiles(path, stringArg(args, providerPatternKey), stringArg(args, providerIncludeKey))
	case providerFindFiles:
		return r.findFiles(path, stringArg(args, "pattern"))
	case providerStatFile:
		return r.statFile(path)
	case providerLSPQuery:
		return r.lspQuery(ctx, args)
	case providerDetectProject:
		return r.detectProject(path)
	case providerRepoContext:
		return r.repoContext(ctx, args)
	default:
		return "", fmt.Errorf("unsupported read-only tool %q", name)
	}
}

func isProviderExplorationTool(name string) bool {
	switch name {
	case providerReadFile, providerMultiRead, providerSearchFiles, providerFindFiles, providerListDir, providerStatFile, providerLSPQuery, providerDetectProject, providerRepoContext:
		return true
	default:
		return false
	}
}

func stringArg(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return value
}

func intArg(args map[string]any, name string, fallback int) int {
	switch value := args[name].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		if n, err := strconv.Atoi(string(value)); err == nil {
			return n
		}
	}
	return fallback
}

func (r *providerReader) readFileRange(path string, startLine, endLine int) (string, error) {
	full, rel, err := r.path(path)
	if err != nil {
		return "", err
	}
	data, truncated, err := readProviderFile(full)
	if err != nil {
		return "", err
	}
	text, err := lineRange(string(data), startLine, endLine)
	if err != nil {
		return "", err
	}
	result := "=== " + rel + " ===\n" + text
	if truncated {
		result += "\n[source read truncated]"
	}
	return r.cap(result), nil
}

func lineRange(data string, startLine, endLine int) (string, error) {
	if startLine < 0 || endLine < 0 || (endLine > 0 && startLine > endLine) {
		return "", errors.New("line range is invalid")
	}
	if startLine == 0 && endLine == 0 {
		return data, nil
	}
	if startLine == 0 {
		startLine = 1
	}
	lines := strings.Split(data, "\n")
	if startLine > len(lines) {
		return "", fmt.Errorf("start_line %d exceeds file length %d", startLine, len(lines))
	}
	if endLine == 0 || endLine > len(lines) {
		endLine = len(lines)
	}
	return strings.Join(lines[startLine-1:endLine], "\n"), nil
}

func (r *providerReader) multiRead(args map[string]any) (string, error) {
	entries, ok := args["files"].([]any)
	if !ok || len(entries) == 0 {
		return "", errors.New("multi_read requires files")
	}
	if len(entries) > 20 {
		return "", errors.New("multi_read accepts at most 20 files")
	}
	var out strings.Builder
	for _, entry := range entries {
		file, ok := entry.(map[string]any)
		if !ok {
			return "", errors.New("multi_read files must be objects")
		}
		path := stringArg(file, providerPathKey)
		if path == "" {
			return "", errors.New("multi_read file path is required")
		}
		section, err := r.readFileRange(path, intArg(file, "start_line", 0), intArg(file, "end_line", 0))
		if err != nil {
			return "", err
		}
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(section)
		if out.Len() >= r.limit {
			break
		}
	}
	return r.cap(out.String()), nil
}

func (r *providerReader) listDirDepth(path string, depth int) (string, error) {
	if depth < 0 {
		return "", errors.New("depth must not be negative")
	}
	files, err := r.files(path)
	if err != nil {
		return "", err
	}
	if depth > 0 {
		base := strings.Trim(filepath.ToSlash(filepath.Clean(path)), ".")
		files = filterDepth(files, base, depth)
	}
	return r.cap(strings.Join(files, "\n")), nil
}

func filterDepth(files []string, base string, depth int) []string {
	kept := files[:0]
	for _, file := range files {
		rel := strings.TrimPrefix(file, strings.TrimSuffix(base, "/")+"/")
		if base == "" {
			rel = file
		}
		if strings.Count(rel, "/") < depth {
			kept = append(kept, file)
		}
	}
	return kept
}

func (r *providerReader) searchFiles(path, pattern, include string) (string, error) {
	if strings.TrimSpace(pattern) == "" {
		return "", errors.New("search_files requires pattern")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid search pattern: %w", err)
	}
	files, err := r.files(path)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, rel := range files {
		if include != "" && !globMatch(include, rel) {
			continue
		}
		fileHits, err := searchFile(rel, re, r)
		if err != nil {
			return "", err
		}
		for _, hit := range fileHits {
			if out.Len()+len(hit)+1 > r.limit {
				out.WriteString("\n[search truncated]")
				return r.cap(out.String()), nil
			}
			if out.Len() > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(hit)
		}
	}
	return r.cap(out.String()), nil
}

func (r *providerReader) findFiles(path, pattern string) (string, error) {
	if strings.TrimSpace(pattern) == "" {
		return "", errors.New("find_files requires pattern")
	}
	files, err := r.files(path)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, rel := range files {
		if globMatch(pattern, rel) {
			matches = append(matches, rel)
		}
	}
	return r.cap(strings.Join(matches, "\n")), nil
}

func globMatch(pattern, target string) bool {
	if ok, err := filepath.Match(pattern, filepath.Base(target)); err == nil && ok {
		return true
	}
	if ok, err := filepath.Match(pattern, target); err == nil && ok {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, "/**"); ok {
		return target == prefix || strings.HasPrefix(target, prefix+"/")
	}
	if prefix, suffix, ok := strings.Cut(pattern, "**/"); ok {
		if matched, err := filepath.Match(suffix, filepath.Base(target)); err == nil && matched {
			return prefix == "" || strings.HasPrefix(target, prefix)
		}
	}
	return false
}

func (r *providerReader) statFile(path string) (string, error) {
	full, rel, err := r.path(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("stat_file requires a regular file")
	}
	result, err := json.Marshal(struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
		Mode string `json:"mode"`
	}{rel, info.Size(), info.Mode().String()})
	return r.cap(string(result)), err
}

func (r *providerReader) lspQuery(ctx context.Context, args map[string]any) (string, error) {
	action, path := stringArg(args, "action"), stringArg(args, providerPathKey)
	full, rel, err := r.path(path)
	if err != nil {
		return "", err
	}
	if action == "diagnostics" {
		return r.lspDiagnostics(ctx, full, rel)
	}
	capability := map[string]string{"definition": lsp.CapabilityDef, "references": lsp.CapabilityRefs, "hover": lsp.CapabilityHover, proposalSymbols: lsp.CapabilitySymbols}[action]
	if capability == "" {
		return "", fmt.Errorf("unknown lsp_query action %q", action)
	}
	line, character := intArg(args, "line", 0), intArg(args, "character", 0)
	if line < 0 || character < 0 {
		return "", errors.New("lsp_query line and character are invalid")
	}
	result, err := lsp.NewService(config.Default().Lsp, r.excludes()).Query(ctx, r.root, lsp.QueryRequest{Capability: capability, File: full, Line: line, Column: character})
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result)
	return r.cap(string(encoded)), err
}

func (r *providerReader) excludes() []string {
	paths := make([]string, 0, len(r.exclude))
	for path := range r.exclude {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func (r *providerReader) detectProject(path string) (string, error) {
	full, rel, err := r.path(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("detect_project requires a directory")
	}
	found := projectMarkers(full)
	files, err := r.files(rel)
	if err != nil {
		return "", err
	}
	languages := projectLanguages(files)
	encoded, err := json.Marshal(struct {
		Path      string         `json:"path"`
		Markers   []string       `json:"markers"`
		Languages map[string]int `json:"languages"`
	}{rel, found, languages})
	return r.cap(string(encoded)), err
}

func projectMarkers(root string) []string {
	markers := []string{"go.mod", "package.json", "Cargo.toml", "pyproject.toml", "requirements.txt", "Gemfile", "pom.xml", "build.gradle", "composer.json"}
	found := make([]string, 0, len(markers))
	for _, marker := range markers {
		if _, statErr := os.Stat(filepath.Join(root, marker)); statErr == nil {
			found = append(found, marker)
		}
	}
	return found
}

func projectLanguages(files []string) map[string]int {
	languages := make(map[string]int)
	for _, file := range files {
		if language := languageForPath(file); language != "" {
			languages[language]++
		}
	}
	return languages
}

func languageForPath(path string) string {
	switch filepath.Ext(path) {
	case ".go":
		return cmdGo
	case ".ts", ".tsx", ".js", ".jsx":
		return "javascript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".php":
		return "php"
	case ".rb":
		return "ruby"
	case ".c", ".h", ".cc", ".cpp":
		return "c_cpp"
	default:
		return ""
	}
}

func (r *providerReader) repoContext(ctx context.Context, args map[string]any) (string, error) {
	action := stringArg(args, "action")
	if action == "" {
		return "", errors.New("repo_context requires action")
	}
	cfg := config.Default()
	cfg.Exclude = append(cfg.Exclude, r.excludes()...)
	snap, err := analyze.Build(ctx, r.root, cfg)
	if err != nil {
		return "", err
	}
	snap = r.filterSnapshot(snap)
	ranked := ranking.Rank(snap, "", ranking.Options{Intent: stringArg(args, "intent")})
	var result any
	switch action {
	case "map":
		result = codemap.Build(ranked, codemap.Options{Tokens: boundedProviderInt(args, "tokens", 4096, 512, 8192), Root: r.root, Edges: snap.Edges})
	case "impact":
		path := stringArg(args, providerPathKey)
		if _, _, pathErr := r.path(path); pathErr != nil {
			return "", pathErr
		}
		impact, ok := retrieval.Impact(ranked, path)
		if !ok {
			return "", fmt.Errorf("no impact data for %q", path)
		}
		result = impact
	case "symbol":
		symbol := stringArg(args, providerSymbolKey)
		if symbol == "" {
			return "", errors.New("repo_context symbol requires symbol")
		}
		symbolContext, contextErr := retrieval.SymbolContext(snap, ranked, symbol, retrieval.SymbolOptions{MaxSourceLines: 120})
		if contextErr != nil {
			return "", contextErr
		}
		result = symbolContext
	case providerAudit:
		result, err = r.repoContextAudit(ctx, snap, args)
	case providerBrief:
		result, err = r.repoContextBrief(ctx, snap, ranked, args)
	default:
		return "", fmt.Errorf("unknown repo_context action %q", action)
	}
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	limit := boundedProviderInt(args, "max_bytes", r.limit, 1000, 40000)
	if limit < r.limit {
		return capProviderBytes(string(encoded), limit), nil
	}
	return r.cap(string(encoded)), nil
}

func (r *providerReader) filterSnapshot(snap analyze.Snapshot) analyze.Snapshot {
	allowed := make(map[string]bool, len(snap.Files))
	files := make([]analyze.File, 0, len(snap.Files))
	for _, file := range snap.Files {
		if deniedProviderPath(file.Path) || r.exclude[file.Path] {
			continue
		}
		allowed[file.Path] = true
		files = append(files, file)
	}
	symbols := make([]analyze.Symbol, 0, len(snap.Symbols))
	for _, symbol := range snap.Symbols {
		if allowed[symbol.Location.Path] {
			symbols = append(symbols, symbol)
		}
	}
	edges := make([]analyze.Edge, 0, len(snap.Edges))
	for _, edge := range snap.Edges {
		if allowed[edge.Location.Path] {
			edges = append(edges, edge)
		}
	}
	snap.Files, snap.Symbols, snap.Edges = files, symbols, edges
	return snap
}

func boundedProviderInt(args map[string]any, name string, fallback, minValue, maxValue int) int {
	n := intArg(args, name, fallback)
	if n < minValue {
		return minValue
	}
	if n > maxValue {
		return maxValue
	}
	return n
}

func capProviderBytes(value string, limit int) string {
	return (&providerReader{limit: limit}).cap(value)
}

func searchFile(rel string, re *regexp.Regexp, reader *providerReader) ([]string, error) {
	full, _, err := reader.path(rel)
	if err != nil {
		return nil, err
	}
	data, truncated, err := readProviderFile(full)
	if err != nil {
		return nil, err
	}
	var hits []string
	used := 0
	for line, text := range strings.Split(string(data), "\n") {
		if re.MatchString(text) {
			hit := fmt.Sprintf("%s:%d:%s", rel, line+1, text)
			if used+len(hit)+1 > reader.limit {
				hits = append(hits, "[search truncated]")
				return hits, nil
			}
			hits = append(hits, hit)
			used += len(hit) + 1
		}
	}
	if truncated {
		hits = append(hits, rel+": [search truncated]")
	}
	return hits, nil
}

func (r *providerReader) cap(value string) string {
	if len(value) <= r.limit {
		return value
	}
	const marker = "\n[tool result truncated]"
	if r.limit <= len(marker) {
		return capUTF8Bytes(value, r.limit)
	}
	return capUTF8Bytes(value, r.limit-len(marker)) + marker
}

func prepPackageSource(reader *providerReader, dir, target string, limit int) (string, error) {
	files, err := reader.files(dir)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, rel := range files {
		if rel == target || !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		full, _, _ := reader.path(rel)
		data, truncated, err := readProviderFile(full)
		if err != nil {
			return "", err
		}
		if truncated {
			b.WriteString("\n[package context truncated]\n")
			break
		}
		section := "\n=== " + rel + " ===\n" + string(data)
		if b.Len()+len(section) > limit {
			b.WriteString("\n[package context truncated]\n")
			break
		}
		b.WriteString(section)
	}
	return b.String(), nil
}
