package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/app"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/lsp"
)

func TestLSPPositionArgsAcceptSourceIdentifier(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n// Target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts, err := (lspPositionArgs{File: "sample.go", Line: 2, Selector: "Target"}).options(root, lsp.CapabilityDef)
	if err != nil {
		t.Fatal(err)
	}
	if opts.File != path || opts.Line != 1 || opts.Column != 3 || opts.Capability != lsp.CapabilityDef {
		t.Fatalf("options = %#v", opts)
	}
}

func TestLSPPositionArgsRetainsNumericColumn(t *testing.T) {
	t.Parallel()
	opts, err := (lspPositionArgs{File: "sample.go", Line: 0, Selector: "4"}).options(".", lsp.CapabilityHover)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Line != 0 || opts.Column != 4 {
		t.Fatalf("options = %#v", opts)
	}
}

func TestLSPCommandsResolveFilesWithinSelectedRepository(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"def", "hover", "refs", "symbols"} {
		for _, form := range []string{"relative", "absolute", "numeric"} {
			if command == "symbols" && form == "numeric" {
				continue
			}
			t.Run(command+"/"+form, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				path := filepath.Join(root, "sample.pan-fixture")
				if err := os.WriteFile(path, []byte("package sample\nfunc Target() {}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				file := "sample.pan-fixture"
				if form == "absolute" {
					file = path
				}
				args := []string{"--repo", root, "--standalone", "--format", "json", "context", "lsp", command, file}
				line, column := 0, 0
				if command != "symbols" {
					line, column = 1, 5
					if form == "numeric" {
						args = append(args, "1", "5")
					} else {
						args = append(args, "2", "Target")
					}
				}
				var out bytes.Buffer
				deps := Deps{App: app.New(app.Deps{Config: config.Default()}), Out: &out, Err: io.Discard}
				if err := Run(context.Background(), args, deps); err != nil {
					t.Fatal(err)
				}
				var envelope struct {
					Result lsp.QueryResult `json:"result"`
				}
				if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				got := envelope.Result
				if got.File != path || got.Line != line || got.Column != column || got.Capability != command {
					t.Fatalf("resolved query = %#v; want %s at %d:%d", got, path, line, column)
				}
				if got.Reason != lsp.ReasonLanguageUnknown {
					t.Fatalf("query did not reach the existing fixture file: %#v", got)
				}
			})
		}
	}
}

func TestLSPCommandsPreserveHomeExpansion(t *testing.T) {
	t.Parallel()
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	root := &Root{}
	parser, err := kong.New(root, kong.Writers(io.Discard, io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse([]string{"--repo", t.TempDir(), "context", "lsp", "def", "~/sample.go", "0", "0"}); err != nil {
		t.Fatal(err)
	}
	opts, err := root.Context.Lsp.Def.options(root.Repo, lsp.CapabilityDef)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := opts.File, filepath.Join(current.HomeDir, "sample.go"); got != want {
		t.Fatalf("home path = %q; want %q", got, want)
	}
}
