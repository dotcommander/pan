package analyze_test

import (
	"context"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestBuildPreservesDistinctTypedCallTargetsOnOneLine(t *testing.T) {
	t.Parallel()
	root := writeTree(t, map[string]string{
		"go.mod":       "module example.test/calls\n\ngo 1.25\n",
		"a/service.go": "package a\ntype Service struct{}\nfunc (Service) Run() {}\n",
		"b/service.go": "package b\ntype Service struct{}\nfunc (Service) Run() {}\n",
		"use.go":       "package calls\nimport (\"example.test/calls/a\"; \"example.test/calls/b\")\nfunc Use() { (a.Service{}).Run(); (b.Service{}).Run() }\n",
	})
	snapshot, err := analyze.Build(context.Background(), root, boundedConfig())
	if err != nil {
		t.Fatal(err)
	}
	resolved := make(map[int]string)
	for _, edge := range snapshot.Edges {
		if edge.Kind != "calls" || edge.Location.Path != "use.go" || edge.To != "Run" {
			continue
		}
		if edge.Confidence != analyze.ConfidenceConfirmed || edge.Target == nil {
			t.Fatalf("unresolved or duplicate lexical call in typed file: %+v", edge)
		}
		if edge.Target.Line != 3 || edge.Location.Column <= 0 {
			t.Fatalf("target or site identity missing: %+v", edge)
		}
		resolved[edge.Location.Column] = edge.Target.Path
	}
	if len(resolved) != 2 {
		t.Fatalf("resolved sites = %#v, want two distinct calls", resolved)
	}
	paths := make(map[string]bool)
	for _, path := range resolved {
		paths[path] = true
	}
	if !paths["a/service.go"] || !paths["b/service.go"] {
		t.Fatalf("targets = %#v, want both declaring files", paths)
	}
}

func TestBuildDoesNotAttributeInterfaceCallToConcreteImplementation(t *testing.T) {
	t.Parallel()
	root := writeTree(t, map[string]string{
		"go.mod":   "module example.test/iface\n\ngo 1.25\n",
		"iface.go": "package iface\ntype Runner interface{ Run() }\ntype Service struct{}\nfunc (Service) Run() {}\nfunc Use(r Runner) { r.Run() }\n",
	})
	snapshot, err := analyze.Build(context.Background(), root, boundedConfig())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, edge := range snapshot.Edges {
		if edge.Kind != "calls" || edge.From != "Use" || edge.To != "Run" {
			continue
		}
		found = true
		if edge.Confidence != analyze.ConfidenceLexical || edge.Target != nil {
			t.Fatalf("interface dispatch was assigned a concrete target: %+v", edge)
		}
	}
	if !found {
		t.Fatal("lexical interface call was lost")
	}
}
