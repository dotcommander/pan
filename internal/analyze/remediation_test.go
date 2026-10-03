package analyze

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dotcommander/pan/internal/config"
)

// frozenEntry reproduces removal after admission metadata has been obtained.
type frozenEntry struct {
	fs.DirEntry
	info fs.FileInfo
}

func (e frozenEntry) Info() (fs.FileInfo, error) { return e.info, nil }

func TestVisitDoesNotAdmitFailedRead(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "gone.ts")
	if err := os.WriteFile(path, []byte("export class Gone {}"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	b := builder{ctx: context.Background(), cfg: config.Default().Normalized(), snap: Snapshot{Root: root, Captured: map[string][]byte{}}, complete: true}
	if err := b.visit(path, frozenEntry{entries[0], info}, nil); err != nil {
		t.Fatal(err)
	}
	if b.complete || len(b.snap.Files) != 0 || len(b.snap.Captured) != 0 || b.total != 0 || len(b.skipped) != 1 {
		t.Fatalf("failed read admitted or complete: %#v", b)
	}
}

func TestExtractionFailureIsIncompleteButRecoverableSyntaxIsNot(t *testing.T) {
	t.Parallel()
	b := builder{ctx: context.Background(), cfg: config.Default().Normalized(), complete: true}
	b.admitParsedSource(File{Path: "broken.ts"}, parsedSource{}, errors.New("extraction failed"))
	if b.complete || len(b.diags) != 1 {
		t.Fatalf("failed extraction: %#v", b)
	}
	recovered := builder{ctx: context.Background(), cfg: config.Default().Normalized(), complete: true}
	recovered.parseSource(File{Path: "recoverable.ts", Language: LanguageTypescript}, []byte("export class Widget {}\nconst broken = ;"))
	if !recovered.complete || len(recovered.snap.Symbols) == 0 {
		t.Fatalf("recoverable syntax lost evidence: %#v", recovered)
	}
}

func TestModulePathsAppendNodeExtensionsWithoutChangingExistingOrder(t *testing.T) {
	t.Parallel()
	want := []string{"widget.ts", "widget/index.ts", "widget.tsx", "widget/index.tsx", "widget.js", "widget/index.js", "widget.jsx", "widget/index.jsx", "widget.mjs", "widget/index.mjs", "widget.cjs", "widget/index.cjs"}
	if got := modulePaths("main.ts", "./widget"); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v", got)
	}
}

func TestCaptureRejectsGrowthBeyondBudget(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("oversized"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCapture(path, 3); err == nil {
		t.Fatal("expected overflow error")
	}
}

func TestNodeModuleExtensionsBindCapturedDeclarations(t *testing.T) {
	t.Parallel()
	for _, ext := range []string{".mjs", ".cjs"} {
		t.Run(ext, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, source := range map[string]string{"widget" + ext: "export class Widget {}", "use.ts": "import { Widget } from './widget';\nconst value = new Widget();"} {
				if err := os.WriteFile(filepath.Join(root, path), []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
			}
			snap, err := Build(context.Background(), root, config.Default())
			if err != nil {
				t.Fatal(err)
			}
			for _, edge := range snap.Edges {
				if edge.Kind == "references" && edge.From == "use.ts" && edge.To == "widget"+ext && edge.Symbol == "Widget" {
					return
				}
			}
			t.Fatalf("captured Node module not resolved: %#v", snap.Edges)
		})
	}
}

func TestBuildCancellationRemainsTerminal(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Build(ctx, t.TempDir(), config.Default()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}
