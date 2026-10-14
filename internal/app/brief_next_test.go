package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/ranking"
)

func TestBriefFollowUpTracesCallableInsteadOfFirstConstant(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for name, source := range map[string]string{
		"go.mod":    "module example.test/brief\n\ngo 1.26.0\n",
		"main.go":   "package sample\nconst Status = 0\nfunc Run() {}\n",
		"first.go":  "package sample\nfunc First() { Run() }\n",
		"second.go": "package sample\nfunc Second() { Run() }\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := New(Deps{Config: config.Default(), SnapshotSource: "live"})
	result, err := service.Brief(context.Background(), root, "trace callers", 4096)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"flow", "calls", "symbol:main.go::Run#function@3", "--depth", "2"}
	if len(result.NextCommands) != 1 || !slices.Equal(result.NextCommands[0].Args, want) {
		t.Fatalf("follow-up = %#v, want %v", result.NextCommands, want)
	}
	_, edges, err := service.Calls(context.Background(), root, result.NextCommands[0].Args[2], 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 {
		t.Fatalf("follow-up returned %d edges, want two incoming calls: %#v", len(edges), edges)
	}
	for _, edge := range edges {
		if edge.To != "Run" || edge.Target == nil || edge.Target.Path != "main.go" || edge.Target.Line != 3 ||
			(edge.From != "First" && edge.From != "Second") {
			t.Fatalf("unexpected follow-up edge: %#v", edge)
		}
	}
}

func TestBriefCallCommandsRequireSymbolSpecificCallerEvidence(t *testing.T) {
	t.Parallel()
	constant := analyze.Symbol{Name: "Status", Kind: "constant", Location: analyze.Location{Path: "owner.go", Line: 1}}
	idle := analyze.Symbol{Name: "Idle", Kind: "function", Location: analyze.Location{Path: "owner.go", Line: 2}}
	active := analyze.Symbol{Name: "Run", Kind: "method", Location: analyze.Location{Path: "owner.go", Line: 3}}
	other := analyze.Symbol{Name: "Run", Kind: "method", Location: analyze.Location{Path: "other.go", Line: 3}}
	symbols := []analyze.Symbol{constant, idle, active}
	call := func(symbol analyze.Symbol, path string, resolved bool) analyze.Edge {
		edge := analyze.Edge{From: "Caller", To: symbol.Name, Kind: "calls", Confidence: analyze.ConfidenceLexical,
			Location: analyze.Location{Path: path, Line: 10}}
		if resolved {
			location := symbol.Location
			edge.Target = &location
			edge.Confidence = analyze.ConfidenceConfirmed
		}
		return edge
	}
	const handle = "symbol:owner.go::Run#method@3"
	cases := []struct {
		name  string
		edges []analyze.Edge
		extra []analyze.Symbol
		want  string
	}{
		{
			name:  "resolved method identity survives name collision",
			edges: []analyze.Edge{call(active, "first.go", true), call(active, "second.go", true)},
			extra: []analyze.Symbol{other},
			want:  handle,
		},
		{
			name:  "unambiguous lexical method has useful callers",
			edges: []analyze.Edge{call(active, "first.go", false), call(active, "second.go", false)},
			want:  handle,
		},
		{
			name:  "ambiguous lexical name cannot credit either method",
			edges: []analyze.Edge{call(active, "first.go", false), call(active, "second.go", false)},
			extra: []analyze.Symbol{other},
		},
		{
			name:  "resolved callers of different declaration do not count",
			edges: []analyze.Edge{call(other, "first.go", true), call(other, "second.go", true)},
			extra: []analyze.Symbol{other},
		},
		{
			name:  "file level aggregate does not imply connected symbol",
			edges: []analyze.Edge{call(idle, "first.go", true), call(active, "second.go", true)},
		},
		{
			name:  "repeat calls from one file count once",
			edges: []analyze.Edge{call(active, "first.go", true), call(active, "first.go", true)},
		},
		{
			name:  "same file calls are not external callers",
			edges: []analyze.Edge{call(active, "owner.go", true), call(active, "first.go", true)},
		},
		{
			name:  "noncallable references cannot produce call recommendation",
			edges: []analyze.Edge{call(constant, "first.go", true), call(constant, "second.go", true)},
		},
		{
			name: "no caller evidence omits recommendation",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			snap := analyze.Snapshot{Symbols: append(slices.Clone(symbols), tc.extra...), Edges: tc.edges}
			ranked := []ranking.RankedFile{{Path: "owner.go", CallerCount: 9, Symbols: symbols}}
			commands := briefCallCommands(snap, ranked)
			if tc.want == "" {
				if len(commands) != 0 {
					t.Fatalf("unsupported recommendation: %#v", commands)
				}
				return
			}
			want := []string{"flow", "calls", tc.want, "--depth", "2"}
			if len(commands) != 1 || !slices.Equal(commands[0].Args, want) {
				t.Fatalf("commands = %#v, want %v", commands, want)
			}
		})
	}
}
