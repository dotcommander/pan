package scan

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestDiffSpecResolvesRevisionSelector(t *testing.T) {
	t.Parallel()
	cases := []struct {
		rev        string
		mode       string
		base, head string
	}{
		{rev: "", mode: DiffModeWorktree, base: "HEAD"},
		{rev: "HEAD", mode: DiffModeWorktree, base: "HEAD"},
		{rev: "abc123..def456", mode: DiffModeCommitted, base: "abc123", head: "def456"},
		{rev: "HEAD~3", mode: DiffModeCommitted, base: "HEAD~3", head: "HEAD"},
	}
	for _, tc := range cases {
		got := diffSpec(tc.rev)
		if got.mode != tc.mode || got.base != tc.base || got.head != tc.head {
			t.Fatalf("diffSpec(%q) = %+v, want mode=%s base=%s head=%s", tc.rev, got, tc.mode, tc.base, tc.head)
		}
	}
}

func TestParseUnifiedDiffClassifiesFilesAndHunks(t *testing.T) {
	t.Parallel()
	fixture := strings.Join([]string{
		"diff --git added.go added.go",
		"new file mode 100644",
		"index 0000000..1111111",
		"--- /dev/null",
		"+++ added.go",
		"@@ -0,0 +1,4 @@",
		"+package a",
		"diff --git gone.go gone.go",
		"deleted file mode 100644",
		"index 1111111..0000000",
		"--- gone.go",
		"+++ /dev/null",
		"@@ -1,3 +0,0 @@",
		"-package g",
		"diff --git mod.go mod.go",
		"index 2222222..3333333 100644",
		"--- mod.go",
		"+++ mod.go",
		"@@ -3,2 +3,3 @@ package m",
		" context",
		"+added line",
		"diff --git old.go new.go",
		"similarity index 90%",
		"rename from old.go",
		"rename to new.go",
		"diff --git logo.png logo.png",
		"index 4444444..5555555 100644",
		"Binary files logo.png and logo.png differ",
	}, "\n")
	files := parseUnifiedDiff(fixture)
	if len(files) != 5 {
		t.Fatalf("files = %d, want 5", len(files))
	}
	byPath := make(map[string]fileDiff, len(files))
	for _, fd := range files {
		key := fd.newPath
		if key == "" {
			key = fd.oldPath
		}
		byPath[key] = fd
	}
	if fd := byPath["added.go"]; fd.status != DiffFileAdded || len(fd.newRanges) != 1 || fd.newRanges[0] != (lineRange{start: 1, end: 5}) {
		t.Fatalf("added.go = %+v", fd)
	}
	if fd := byPath["gone.go"]; fd.status != DiffFileRemoved || len(fd.oldRanges) != 1 {
		t.Fatalf("gone.go = %+v", fd)
	}
	if fd := byPath["mod.go"]; fd.status != DiffFileModified || len(fd.newRanges) != 1 || fd.newRanges[0] != (lineRange{start: 3, end: 6}) {
		t.Fatalf("mod.go = %+v", fd)
	}
	if fd := byPath["new.go"]; fd.status != DiffFileRenamed || fd.oldPath != "old.go" {
		t.Fatalf("new.go = %+v", fd)
	}
	if fd := byPath["logo.png"]; !fd.binary {
		t.Fatalf("logo.png = %+v", fd)
	}
}

func TestParseCountSpecAnchorsZeroCounts(t *testing.T) {
	t.Parallel()
	got, ok := parseCountSpec("-5,0")
	if !ok || got != (lineRange{start: 5, end: 6}) {
		t.Fatalf("parseCountSpec(-5,0) = %+v ok=%v", got, ok)
	}
	got, ok = parseCountSpec("+12,3")
	if !ok || got != (lineRange{start: 12, end: 15}) {
		t.Fatalf("parseCountSpec(+12,3) = %+v ok=%v", got, ok)
	}
	if _, ok := parseCountSpec("x1"); ok {
		t.Fatal("parseCountSpec accepted malformed spec")
	}
}

func symbolAt(name, kind, signature string, line, end int) analyze.Symbol {
	return analyze.Symbol{Name: name, Kind: kind, Signature: signature, Location: analyze.Location{Line: line}, EndLine: end}
}

func TestClassifyFileDetectsSignatureBodyAddedRemoved(t *testing.T) {
	t.Parallel()
	before := []analyze.Symbol{
		symbolAt("Same", "function", "(int)", 1, 10),
		symbolAt("Bodied", "function", "(int)", 11, 20),
		symbolAt("Gone", "function", "(int)", 21, 30),
	}
	after := []analyze.Symbol{
		symbolAt("Same", "function", "(int)", 1, 10),
		symbolAt("Bodied", "function", "(int)", 11, 22),
		symbolAt("Fresh", "function", "(int)", 23, 26),
	}
	fd := fileDiff{
		status: DiffFileModified,
		oldRanges: []lineRange{
			{start: 12, end: 18}, // Bodied body
			{start: 21, end: 25}, // Gone removal region
		},
		newRanges: []lineRange{
			{start: 12, end: 20}, // Bodied body
			{start: 23, end: 26}, // Fresh addition
		},
	}
	symbols := classifyFile(fd, before, after)
	byName := make(map[string]DiffSymbol, len(symbols))
	for _, entry := range symbols {
		byName[entry.Name] = entry
	}
	if entry, ok := byName["Same"]; ok {
		t.Fatalf("unchanged symbol reported: %+v", entry)
	}
	if entry := byName["Bodied"]; entry.Change != DiffChangeBody || entry.Confidence != ConfidenceMedium {
		t.Fatalf("Bodied = %+v", entry)
	}
	if entry := byName["Gone"]; entry.Change != DiffChangeRemoved || entry.BeforeLine != 21 || entry.Confidence != ConfidenceHigh {
		t.Fatalf("Gone = %+v", entry)
	}
	if entry := byName["Fresh"]; entry.Change != DiffChangeAdded || entry.AfterLine != 23 {
		t.Fatalf("Fresh = %+v", entry)
	}

	// A signature change on the same overlapping symbol is high confidence.
	changed := []analyze.Symbol{symbolAt("Same", "function", "(string)", 1, 10)}
	fd = fileDiff{status: DiffFileModified, oldRanges: []lineRange{{start: 1, end: 3}}, newRanges: []lineRange{{start: 1, end: 3}}}
	symbols = classifyFile(fd, before, changed)
	if len(symbols) != 1 || symbols[0].Change != DiffChangeSignature || symbols[0].Confidence != ConfidenceHigh {
		t.Fatalf("signature classification = %#v", symbols)
	}
}

func TestDetectCrossFileMovesPairsUniqueRemovalsAndAdditions(t *testing.T) {
	t.Parallel()
	files := []DiffFile{
		{Path: "a/old.go", Status: DiffFileModified, Symbols: []DiffSymbol{
			{Name: "Helper", Kind: "function", Change: DiffChangeRemoved, BeforeLine: 4, Confidence: ConfidenceHigh},
			{Name: "Keep", Kind: "function", Change: DiffChangeBody, Confidence: ConfidenceMedium},
		}},
		{Path: "b/new.go", Status: DiffFileModified, Symbols: []DiffSymbol{
			{Name: "Helper", Kind: "function", Change: DiffChangeAdded, AfterLine: 7, Confidence: ConfidenceHigh},
		}},
	}
	files, _ = detectCrossFileMoves(files, nil)
	if len(files[0].Symbols) != 1 || files[0].Symbols[0].Name != "Keep" {
		t.Fatalf("removed counterpart not dropped: %#v", files[0].Symbols)
	}
	if len(files[1].Symbols) != 1 {
		t.Fatalf("moved entry missing: %#v", files[1].Symbols)
	}
	moved := files[1].Symbols[0]
	if moved.Change != DiffChangeMoved || moved.MovedFrom != "a/old.go" || moved.Confidence != ConfidenceMedium || moved.AfterLine != 7 {
		t.Fatalf("moved = %+v", moved)
	}
}

func TestDetectCrossFileMovesLeavesAmbiguousNamesAlone(t *testing.T) {
	t.Parallel()
	files := []DiffFile{
		{Path: "a/one.go", Symbols: []DiffSymbol{{Name: "Dup", Kind: "function", Change: DiffChangeRemoved}}},
		{Path: "a/two.go", Symbols: []DiffSymbol{{Name: "Dup", Kind: "function", Change: DiffChangeRemoved}}},
		{Path: "b/in.go", Symbols: []DiffSymbol{{Name: "Dup", Kind: "function", Change: DiffChangeAdded}}},
	}
	files, _ = detectCrossFileMoves(files, nil)
	changes := []string{files[0].Symbols[0].Change, files[1].Symbols[0].Change, files[2].Symbols[0].Change}
	for _, change := range changes {
		if change != DiffChangeRemoved && change != DiffChangeAdded {
			t.Fatalf("ambiguous pairing reclassified: %#v", files)
		}
	}
}

// newDiffTestRepo creates one git repository with a committed base file and
// returns its directory.
func newDiffTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	base := "package base\n\nfunc Keep() int { return 1 }\n\nfunc Helper(x int) int { return x }\n"
	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "base.go")
	run("commit", "-q", "-m", "base")
	return dir
}

func TestStructuralDiffWorktreeModeClassifiesSymbols(t *testing.T) {
	t.Parallel()
	dir := newDiffTestRepo(t)
	// Modify Helper's signature in place and remove Keep.
	updated := "package base\n\nfunc Helper(x int, y int) int { return x + y }\n"
	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := StructuralDiff(context.Background(), dir, "", DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != DiffModeWorktree || report.Base != "HEAD" {
		t.Fatalf("mode = %+v", report)
	}
	if len(report.Files) != 1 || report.Files[0].Path != "base.go" {
		t.Fatalf("files = %#v", report.Files)
	}
	byName := make(map[string]DiffSymbol)
	for _, entry := range report.Files[0].Symbols {
		byName[entry.Name] = entry
	}
	if entry := byName["Helper"]; entry.Change != DiffChangeSignature || entry.Confidence != ConfidenceHigh {
		t.Fatalf("Helper = %+v", entry)
	}
	if entry := byName["Keep"]; entry.Change != DiffChangeRemoved || entry.BeforeLine != 3 {
		t.Fatalf("Keep = %+v", entry)
	}
}

func TestStructuralDiffCommittedRangeAndCrossFileMove(t *testing.T) {
	t.Parallel()
	dir := newDiffTestRepo(t)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Remove Helper from base.go and add the same declaration in util.go.
	base := "package base\n\nfunc Keep() int { return 1 }\n"
	util := "package util\n\nfunc Helper(x int) int { return x }\n"
	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "util.go"), []byte(util), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "base.go", "util.go")
	run("commit", "-q", "-m", "move helper")
	report, err := StructuralDiff(context.Background(), dir, "HEAD~1", DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != DiffModeCommitted || report.Base != "HEAD~1" || report.Head != "HEAD" {
		t.Fatalf("mode = %+v", report)
	}
	var moved *DiffSymbol
	for i, file := range report.Files {
		for j, entry := range file.Symbols {
			if entry.Change == DiffChangeMoved {
				if moved != nil {
					t.Fatalf("multiple moved entries: %#v", report.Files)
				}
				moved = &report.Files[i].Symbols[j]
			}
		}
	}
	if moved == nil {
		t.Fatalf("no moved symbol in %#v", report.Files)
	}
	if moved.Name != "Helper" || moved.MovedFrom != "base.go" || moved.AfterLine != 3 {
		t.Fatalf("moved = %+v", moved)
	}
}

func TestBoundedNotesSortsDedupesAndCaps(t *testing.T) {
	t.Parallel()
	notes := boundedNotes([]string{"b", "a", "b"})
	if len(notes) != 2 || notes[0] != "a" || notes[1] != "b" {
		t.Fatalf("notes = %#v", notes)
	}
	if boundedNotes(nil) != nil {
		t.Fatalf("nil notes = %#v", boundedNotes(nil))
	}
	var many []string
	for i := 0; i < diffMaxNotes+5; i++ {
		many = append(many, "note")
	}
	many = boundedNotes(many)
	if len(many) != 1 { // identical notes dedupe to one
		t.Fatalf("many = %#v", many)
	}
	var distinct []string
	for i := 0; i < diffMaxNotes+5; i++ {
		distinct = append(distinct, fmt.Sprintf("note-%02d", i))
	}
	capped := boundedNotes(distinct)
	if len(capped) != diffMaxNotes { // the cap counts the summary line
		t.Fatalf("capped len = %d, want %d", len(capped), diffMaxNotes)
	}
	if last := capped[len(capped)-1]; last != "... (6 more notes)" {
		t.Fatalf("summary = %q", last)
	}
}

func TestUnquoteGitPathDecodesGitCQuoting(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"plain.go", "plain.go"},
		{`"we ird\ttab.go"`, "we ird\ttab.go"},
		{`"caf\303\251.go"`, "café.go"}, // é as UTF-8 octal bytes
		{`"quote\"d.go"`, `quote"d.go`},
		{`"unknown\x41.go"`, `unknown\x41.go`}, // unknown escape kept literally
	}
	for _, tc := range cases {
		if got := unquoteGitPath(tc.in); got != tc.want {
			t.Fatalf("unquoteGitPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseUnifiedDiffResolvesQuotedPaths(t *testing.T) {
	t.Parallel()
	out := strings.Join([]string{
		`diff --git "odd name.bin" "odd name.bin"`,
		"Binary files differ",
		"diff --git base.go renamed.go",
		"rename from \"has space.go\"",
		"rename to \"renamed\303\250d.go\"",
		"",
	}, "\n")
	files := parseUnifiedDiff(out)
	if len(files) != 2 {
		t.Fatalf("files = %#v", files)
	}
	if files[0].oldPath != "odd name.bin" || files[0].newPath != "odd name.bin" || !files[0].binary {
		t.Fatalf("quoted binary paths = %#v", files[0])
	}
	if files[1].oldPath != "has space.go" || files[1].newPath != "renamedèd.go" || files[1].status != DiffFileRenamed {
		t.Fatalf("quoted rename paths = %#v", files[1])
	}
}

func TestStructuralDiffRejectsSymmetricRange(t *testing.T) {
	t.Parallel()
	dir := newDiffTestRepo(t)
	_, err := StructuralDiff(context.Background(), dir, "HEAD...HEAD~1", DiffOptions{})
	if err == nil || !strings.Contains(err.Error(), "symmetric") {
		t.Fatalf("err = %v, want symmetric-range rejection", err)
	}
}

func TestStructuralDiffNormalizesOpenEndedRange(t *testing.T) {
	t.Parallel()
	dir := newDiffTestRepo(t)
	report, err := StructuralDiff(context.Background(), dir, "HEAD..", DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != DiffModeCommitted || report.Head != "HEAD" {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Files) != 0 { // HEAD..HEAD compares a commit with itself
		t.Fatalf("files = %#v", report.Files)
	}
}
