package improve

import (
	"context"
	"strings"
	"testing"
)

func TestBuildCandidatePacketRanksPrivateLowReferenceSymbols(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", fixtureGoMod)
	writeFile(t, root, "pkg/x/x.go", `package x

func used() string { return helper() }

func helper() string { return "ok" }

func lonely() int { return 1 }

func Exported() int { return lonely() }
`)
	writeFile(t, root, "pkg/x/x_test.go", "package x\n\nfunc TestX(t *testing.T) {}\n")

	packet, err := BuildCandidatePacket(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(packet.Files) == 0 {
		t.Fatalf("missing candidates: %#v", packet)
	}
	got := packet.Files[0]
	if got.Path != "pkg/x/x.go" || got.Lane != "refactor-surface" {
		t.Fatalf("candidate file = %#v", got)
	}
	if got.Confidence != "high" || got.Actionability != "likely_defect" {
		t.Fatalf("candidate confidence/actionability = %q/%q", got.Confidence, got.Actionability)
	}
	for _, want := range []string{
		"candidate func", "pkg/x/x.go:", "lexical references outside declaration:",
		"lexical references in tests: 0", "declaration: func", "score:",
	} {
		if !strings.Contains(strings.Join(got.Reasons, "\n"), want) {
			t.Fatalf("candidate reasons missing %q: %#v", want, got.Reasons)
		}
	}
	if want := []string{"ast_symbol_scan", "lexical_reference_count", "package_local_surface"}; strings.Join(got.EvidenceLayers, ",") != strings.Join(want, ",") {
		t.Fatalf("evidence layers = %#v", got.EvidenceLayers)
	}
	if len(got.Verify) != 1 || got.Verify[0] != "go test ./pkg/x" {
		t.Fatalf("verify = %#v", got.Verify)
	}
	if want := []string{"semantic_references_not_checked", "git_history_not_checked"}; strings.Join(packet.SkippedSignals, ",") != strings.Join(want, ",") {
		t.Fatalf("skipped signals = %#v", packet.SkippedSignals)
	}
}

func TestBuildCandidatePacketHonorsExcludeAndNoGoFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", fixtureGoMod)
	writeFile(t, root, "ignored/demo.go", "package ignored\n\nfunc lonely() {}\n")
	writeFile(t, root, "notes.txt", "not Go\n")

	packet, err := BuildCandidatePacket(context.Background(), root, []string{"ignored"})
	if err != nil {
		t.Fatal(err)
	}
	if len(packet.Files) != 0 || len(packet.SkippedSignals) != 1 || packet.SkippedSignals[0] != "no_go_files_found" {
		t.Fatalf("packet = %#v", packet)
	}
}

func TestBuildCandidatePacketCapsDistinctFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", fixtureGoMod)
	for i := 0; i < maxCandidateFiles+3; i++ {
		name := "pkg/f" + string(rune('a'+i)) + ".go"
		writeFile(t, root, name, "package pkg\n\nfunc private"+string(rune('a'+i))+"() {}\n")
	}

	packet, err := BuildCandidatePacket(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(packet.Files) != maxCandidateFiles {
		t.Fatalf("candidate files = %d, want %d", len(packet.Files), maxCandidateFiles)
	}
}
