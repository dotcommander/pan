package codemap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

func TestBuildFormatsCarryDistinctContent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n// Alpha doc\nfunc Alpha() {}\nfunc beta() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ranked := []ranking.RankedFile{{Path: "a.go", Language: "go", ImportedBy: 2, DetailLevel: 2, Symbols: []analyze.Symbol{{Name: "Alpha", Kind: "function", Exported: true, Signature: "()", Doc: "Alpha doc", Location: analyze.Location{Line: 3}}, {Name: "beta", Kind: "function", Location: analyze.Location{Line: 4}}}, Components: map[string]int{"symbols": 4}}}
	verbose := Build(ranked, Options{Mode: ModeVerbose, Root: root}).Text
	detail := Build(ranked, Options{Mode: ModeDetail, Root: root}).Text
	lines := Build(ranked, Options{Mode: ModeLines, Root: root}).Text
	xml := Build(ranked, Options{Mode: ModeXML, Root: root}).Text
	if strings.Contains(verbose, "Alpha doc") {
		t.Fatalf("verbose should list symbols without docs: %q", verbose)
	}
	if !strings.Contains(detail, "Alpha doc") {
		t.Fatalf("detail should include docs: %q", detail)
	}
	if !strings.Contains(lines, "func Alpha()") {
		t.Fatalf("lines should include source: %q", lines)
	}
	if !strings.Contains(xml, "<repomap") || !strings.Contains(xml, `selected-files="1"`) || !strings.Contains(xml, "symbol") {
		t.Fatalf("xml document missing map symbols: %q", xml)
	}
}

func TestBuildCallEvidenceRespectsTestsAndExplain(t *testing.T) {
	t.Parallel()
	ranked := []ranking.RankedFile{{Path: "a.go", ImportedBy: 2, Symbols: []analyze.Symbol{{Name: "Alpha", Exported: true}}, Components: map[string]int{"symbols": 4}}}
	edges := []analyze.Edge{{From: "Use", To: "Alpha", Kind: "calls", Confidence: analyze.ConfidenceConfirmed, Location: analyze.Location{Path: "use.go", Line: 4}}, {From: "Test", To: "Alpha", Kind: "calls", Confidence: analyze.ConfidenceConfirmed, Location: analyze.Location{Path: "a_test.go", Line: 5}}}
	plain := Build(ranked, Options{Mode: ModeVerbose, Calls: true, CallsThreshold: 2, Edges: edges, ExplainScores: true})
	if len(plain.CallEvidence) != 1 || strings.Contains(plain.Text, "a_test.go") || !strings.Contains(plain.Text, "[score 0: symbols=4]") {
		t.Fatalf("unexpected filtered call result: %#v %q", plain.CallEvidence, plain.Text)
	}
	withTests := Build(ranked, Options{Mode: ModeVerbose, Calls: true, CallsThreshold: 2, CallsIncludeTests: true, Edges: edges})
	if len(withTests.CallEvidence) != 2 {
		t.Fatalf("calls include tests = %#v", withTests.CallEvidence)
	}
}

func TestBuildKeepsDemotedTestsAndDropsUnknownFiles(t *testing.T) {
	t.Parallel()
	ranked := []ranking.RankedFile{
		{Path: "main.go", Language: "go", Symbols: []analyze.Symbol{{Name: "Main", Kind: "function"}}},
		{Path: "main_test.go", Language: "go", TestFile: true, Symbols: []analyze.Symbol{{Name: "TestMain", Kind: "function"}}},
		{Path: "go.mod", Language: languageUnknown},
	}
	result := Build(ranked, Options{Mode: ModeCompact})
	if result.TotalFiles != 2 || !strings.Contains(result.Text, "main_test.go") || strings.Contains(result.Text, "go.mod") {
		t.Fatalf("map selection = %#v\n%s", result, result.Text)
	}
}
