package codemap

import (
	"maps"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

// StructuredOutput is Pan's machine-readable map format.
// It reports only the files selected for the requested token budget while
// retaining complete repository totals and selection accounting.
type StructuredOutput struct {
	SchemaVersion int              `json:"schema_version"`
	Root          string           `json:"root"`
	Totals        StructuredTotals `json:"totals"`
	Config        StructuredConfig `json:"config"`
	Coverage      ParseCoverage    `json:"coverage"`
	Warnings      []string         `json:"warnings,omitempty"`
	Selection     OutputSelection  `json:"selection"`
	Files         []StructuredFile `json:"files"`
}

type StructuredTotals struct {
	Files   int `json:"files"`
	Symbols int `json:"symbols"`
}

type StructuredConfig struct {
	MaxTokens      int      `json:"max_tokens"`
	MaxTokensNoCtx int      `json:"max_tokens_no_ctx"`
	Intent         string   `json:"intent,omitempty"`
	ConsumedPaths  []string `json:"consumed_paths,omitempty"`
	SymbolRefs     bool     `json:"symbol_refs,omitempty"`
}

// ParseCoverage reports the parser fidelity Pan can establish from its
// snapshot. Fields that require a Go build context or ctags stay at zero.
type ParseCoverage struct {
	FilesScanned      int            `json:"files_scanned"`
	FilesParsed       int            `json:"files_parsed"`
	ParseFailures     int            `json:"parse_failures,omitempty"`
	ByLanguage        map[string]int `json:"by_language,omitempty"`
	ByParseMethod     map[string]int `json:"by_parse_method,omitempty"`
	FailuresByLang    map[string]int `json:"failures_by_language,omitempty"`
	TreeSitterEnabled bool           `json:"tree_sitter_enabled"`
	CtagsEnabled      bool           `json:"ctags_enabled"`
	GoSemanticActive  int            `json:"go_semantic_active,omitempty"`
	GoSyntaxInactive  int            `json:"go_syntax_inactive,omitempty"`
	GoAnalysisFailed  int            `json:"go_analysis_failed,omitempty"`
}

type OutputSelection struct {
	TotalFiles      int    `json:"total_files"`
	TotalSymbols    int    `json:"total_symbols"`
	SelectedFiles   int    `json:"selected_files"`
	SelectedSymbols int    `json:"selected_symbols"`
	OmittedFiles    int    `json:"omitted_files"`
	OmittedSymbols  int    `json:"omitted_symbols"`
	OmittedReason   string `json:"omitted_reason,omitempty"`
}

type StructuredFile struct {
	Path             string               `json:"path"`
	Handle           string               `json:"handle,omitempty"`
	Language         string               `json:"language,omitempty"`
	CapabilityTier   string               `json:"capability_tier,omitempty"`
	Package          string               `json:"package,omitempty"`
	ImportPath       string               `json:"import_path,omitempty"`
	ParseMethod      string               `json:"parse_method,omitempty"`
	BuildActive      bool                 `json:"build_active,omitempty"`
	AnalysisMode     string               `json:"analysis_mode,omitempty"`
	Score            int                  `json:"score"`
	ScoreComponents  map[string]int       `json:"score_components,omitempty"`
	DetailLevel      int                  `json:"detail_level"`
	ImportedBy       int                  `json:"imported_by,omitempty"`
	DependsOn        int                  `json:"depends_on,omitempty"`
	Untested         bool                 `json:"untested,omitempty"`
	Boundaries       []string             `json:"boundaries,omitempty"`
	Imports          []string             `json:"imports,omitempty"`
	RelationEvidence []StructuredEvidence `json:"relation_evidence,omitempty"`
	Symbols          []StructuredSymbol   `json:"symbols,omitempty"`
	CallSites        []StructuredCallSite `json:"call_sites,omitempty"`
	OmittedReason    string               `json:"omitted_reason,omitempty"`
}

type StructuredEvidence struct {
	Kind          string `json:"kind"`
	EvidenceClass string `json:"evidence_class"`
	Confidence    string `json:"confidence"`
	Detail        string `json:"detail"`
	Caveat        string `json:"caveat,omitempty"`
}

type StructuredSymbol struct {
	Name        string   `json:"name"`
	Handle      string   `json:"handle,omitempty"`
	FileHandle  string   `json:"file_handle,omitempty"`
	Kind        string   `json:"kind"`
	Signature   string   `json:"signature,omitempty"`
	Receiver    string   `json:"receiver,omitempty"`
	Exported    bool     `json:"exported,omitempty"`
	Dead        bool     `json:"dead,omitempty"`
	Line        int      `json:"line,omitempty"`
	EndLine     int      `json:"end_line,omitempty"`
	ParamCount  int      `json:"param_count,omitempty"`
	ResultCount int      `json:"result_count,omitempty"`
	Implements  []string `json:"implements,omitempty"`
	Doc         string   `json:"doc,omitempty"`
	Hash        string   `json:"hash,omitempty"`
}

type StructuredCallSite struct {
	Name string `json:"name"`
	Line int    `json:"line,omitempty"`
}

// BuildStructured renders a map and returns its structured representation.
// Build mutates detail levels while allocating the budget, so it receives a
// private ranking copy and leaves the caller's ranked slice reusable.
func BuildStructured(snap analyze.Snapshot, ranked []ranking.RankedFile, opts Options) StructuredOutput {
	working := slices.DeleteFunc(slices.Clone(ranked), func(file ranking.RankedFile) bool {
		return file.Language == languageUnknown
	})
	result := Build(working, opts)
	selected := selectedFiles(working)
	files := make([]StructuredFile, 0, len(selected))
	for _, file := range selected {
		files = append(files, structuredFile(file))
	}
	return StructuredOutput{
		SchemaVersion: 2,
		Root:          snap.Root,
		Totals:        StructuredTotals{Files: result.TotalFiles, Symbols: result.TotalSymbols},
		Config: StructuredConfig{
			MaxTokens: result.TokenBudget, Intent: opts.Intent,
			ConsumedPaths: append([]string(nil), opts.Consumed...), SymbolRefs: opts.SymbolRefs,
		},
		Coverage:  coverage(snap),
		Selection: selection(result),
		Files:     files,
	}
}

func selectedFiles(ranked []ranking.RankedFile) []ranking.RankedFile {
	selected := make([]ranking.RankedFile, 0, len(ranked))
	for _, file := range ranked {
		if file.Language != languageUnknown && file.DetailLevel >= 0 {
			selected = append(selected, file)
		}
	}
	return selected
}

func selection(result Result) OutputSelection {
	selection := OutputSelection{
		TotalFiles: result.TotalFiles, TotalSymbols: result.TotalSymbols,
		SelectedFiles: result.ShownFiles, SelectedSymbols: result.ShownSymbols,
		OmittedFiles: result.OmittedFiles, OmittedSymbols: result.TotalSymbols - result.ShownSymbols,
	}
	if selection.OmittedFiles > 0 || selection.OmittedSymbols > 0 {
		selection.OmittedReason = "token budget"
	}
	return selection
}

func coverage(snap analyze.Snapshot) ParseCoverage {
	coverage := ParseCoverage{
		FilesScanned: len(snap.Files), ByLanguage: make(map[string]int),
		ByParseMethod: make(map[string]int), TreeSitterEnabled: true,
	}
	for _, file := range snap.Files {
		coverage.ByLanguage[file.Language]++
		method := parseMethod(file.Language)
		if method == "" {
			continue
		}
		coverage.FilesParsed++
		coverage.ByParseMethod[method]++
	}
	for _, diagnostic := range snap.Diagnostics {
		if diagnostic.Level == "warning" {
			coverage.ParseFailures++
		}
	}
	return coverage
}

func structuredFile(file ranking.RankedFile) StructuredFile {
	path := filepath.ToSlash(file.Path)
	return StructuredFile{
		Path: path, Handle: fileHandle(path), Language: file.Language,
		CapabilityTier: capabilityTier(file.Language), Package: file.Package,
		ParseMethod: parseMethod(file.Language), Score: file.Score,
		ScoreComponents: cloneComponents(file.Components), DetailLevel: file.DetailLevel,
		ImportedBy: file.ImportedBy, DependsOn: file.DependsOn,
		Untested:         !file.TestFile && !file.Tested && file.Language == languageGo,
		Imports:          append([]string(nil), file.Imports...),
		RelationEvidence: relationEvidence(file), Symbols: structuredSymbols(path, file.Symbols),
	}
}

func relationEvidence(file ranking.RankedFile) []StructuredEvidence {
	if file.ImportedBy == 0 {
		return nil
	}
	return []StructuredEvidence{{
		Kind: "import_reference", EvidenceClass: "import_graph", Confidence: analyze.ConfidenceConfirmed,
		Detail: "Scanned import edges resolve to this repository package",
	}}
}

func structuredSymbols(path string, symbols []analyze.Symbol) []StructuredSymbol {
	if len(symbols) == 0 {
		return nil
	}
	out := make([]StructuredSymbol, 0, len(symbols))
	for _, symbol := range symbols {
		out = append(out, StructuredSymbol{
			Name: symbol.Name, Handle: symbolHandle(path, symbol), FileHandle: fileHandle(path),
			Kind: symbol.Kind, Signature: symbol.Signature, Receiver: symbol.Receiver,
			Exported: symbol.Exported, Line: symbol.Location.Line, EndLine: symbol.EndLine, Doc: symbol.Doc,
		})
	}
	return out
}

func cloneComponents(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	return maps.Clone(in)
}

func fileHandle(path string) string { return "file:" + path }

func symbolHandle(path string, symbol analyze.Symbol) string {
	if path == "" || symbol.Name == "" || symbol.Location.Line <= 0 {
		return ""
	}
	return "symbol:" + path + "::" + symbol.Name + "#" + symbol.Kind + "@" + strconv.Itoa(symbol.Location.Line)
}

func parseMethod(language string) string {
	if language == languageGo {
		return "go_ast"
	}
	if treeSitterLanguage(language) {
		return "tree_sitter"
	}
	return ""
}

func capabilityTier(language string) string {
	if parseMethod(language) != "" {
		return "syntax"
	}
	return "unknown"
}

func treeSitterLanguage(language string) bool {
	switch language {
	case "c", "cpp", "java", "php", "python", "ruby", "rust", "typescript", "javascript", "tsx", "jsx":
		return true
	default:
		return false
	}
}
