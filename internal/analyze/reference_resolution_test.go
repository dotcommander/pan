package analyze_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestExplicitImportsBindOnlyTheirCapturedDeclarations(t *testing.T) {
	t.Parallel()
	root := writeTree(t, map[string]string{
		"left.py":     "class Duplicate:\n    pass\n",
		"right.py":    "class Duplicate:\n    pass\n",
		"consumer.py": "from left import Duplicate as Choice\ndef build():\n    return Choice()\n",
		"left.ts":     "export class Page {}\n",
		"right.ts":    "export class Page {}\n",
		"consumer.ts": "import { Page as View } from './left'\nexport function render() { return new View() }\n",
	})
	snapshot, err := analyze.Build(context.Background(), root, boundedConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ from, to, symbol string }{
		{"consumer.py", "left.py", "Duplicate"},
		{"consumer.ts", "left.ts", "Page"},
	} {
		found := false
		for _, edge := range snapshot.Edges {
			if edge.Kind != "references" || edge.From != want.from {
				continue
			}
			if edge.To != want.to || edge.Symbol != want.symbol || edge.Confidence != analyze.ConfidenceSyntactic {
				t.Fatalf("false reference attribution: %+v, want %+v", edge, want)
			}
			found = true
		}
		if !found {
			t.Fatalf("missing proved import %+v; edges=%+v, unresolved=%+v", want, snapshot.Edges, snapshot.UnresolvedReferences)
		}
	}
}

func TestUnboundAndShadowedNamesRemainUnresolved(t *testing.T) {
	t.Parallel()
	root := writeTree(t, map[string]string{
		"left.py":     "class Duplicate:\n    pass\n",
		"right.py":    "class Duplicate:\n    pass\n",
		"unbound.py":  "def build():\n    return Duplicate()\n",
		"model.ts":    "export class Page {}\n",
		"shadowed.ts": "import { Page } from './model'\nexport function build(Page: unknown) { return Page }\n",
	})
	snapshot, err := analyze.Build(context.Background(), root, boundedConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == "references" && (edge.From == "unbound.py" || edge.From == "shadowed.ts") {
			t.Fatalf("unproved dependency became an edge: %+v", edge)
		}
	}
	for _, want := range []struct{ from, name, reason string }{
		{"unbound.py", "Duplicate", "no_import_binding"},
		{"shadowed.ts", "Page", "shadowed"},
	} {
		found := false
		for _, unresolved := range snapshot.UnresolvedReferences {
			if unresolved.From == want.from && unresolved.Name == want.name && unresolved.Reason == want.reason {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing unresolved %+v; got %+v", want, snapshot.UnresolvedReferences)
		}
	}
	if !snapshot.Status.Complete {
		t.Fatal("binding ambiguity must not mark discovery incomplete")
	}
}

func TestUnboundReferenceDoesNotSuggestUnrelatedLanguage(t *testing.T) {
	t.Parallel()
	root := writeTree(t, map[string]string{
		"go.mod":        "module example.com/language-candidate\n\ngo 1.26\n",
		"definition.go": "package demo\nfunc Worker() {}\n",
		"consumer.py":   "def build():\n    return Worker()\n",
	})
	snapshot, err := analyze.Build(context.Background(), root, boundedConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range snapshot.UnresolvedReferences {
		if item.From == "consumer.py" && item.Name == "Worker" {
			t.Fatalf("Python reference suggested a Go declaration: %+v", item)
		}
	}
}

func TestRepeatedImportBindingDoesNotCreditOneCapturedCandidate(t *testing.T) {
	t.Parallel()
	root := writeTree(t, map[string]string{
		"target.py":   "class Worker:\n    pass\n",
		"consumer.py": "from target import Worker\nfrom missing import Worker\ndef build():\n    return Worker()\n",
	})
	snapshot, err := analyze.Build(context.Background(), root, boundedConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == "references" && edge.From == "consumer.py" {
			t.Fatalf("ambiguous import credited one candidate: %+v", edge)
		}
	}
	found := false
	for _, unresolved := range snapshot.UnresolvedReferences {
		if unresolved.From == "consumer.py" && unresolved.Name == "Worker" && unresolved.Reason == "ambiguous_binding" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing import ambiguity: %+v", snapshot.UnresolvedReferences)
	}
}

func TestPartialAndCappedReferenceEvidenceDoesNotClaimAbsence(t *testing.T) {
	t.Parallel()
	root := writeTree(t, map[string]string{
		"consumer.py": "from target import Worker\ndef build():\n    return Worker()\n",
		"target.py":   "class Worker:\n    pass\n",
	})
	cfg := boundedConfig()
	cfg.MaxFiles = 1
	snapshot, err := analyze.Build(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status.Complete || snapshot.UnresolvedCount == 0 {
		t.Fatalf("partial target was treated as complete: %+v", snapshot)
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == "references" {
			t.Fatalf("missing target was guessed: %+v", edge)
		}
	}

	root = writeTree(t, map[string]string{
		"definition.py": "class Worker:\n    pass\n",
		"consumer.py":   "def build():\n" + strings.Repeat("    Worker()\n", 80),
	})
	snapshot, err = analyze.Build(context.Background(), root, boundedConfig())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.UnresolvedCount != 80 || len(snapshot.UnresolvedReferences) != 64 || snapshot.UnresolvedTruncation == nil || snapshot.UnresolvedTruncation.Total != 80 {
		t.Fatalf("unresolved ledger not bounded or truthful: count=%d shown=%d cap=%+v", snapshot.UnresolvedCount, len(snapshot.UnresolvedReferences), snapshot.UnresolvedTruncation)
	}
}
