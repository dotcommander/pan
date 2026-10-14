package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/cli"
)

func TestCleanDepthCoverageEnvelopes(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("CLI fixture uses local git")
	}
	for _, leaf := range [][]string{{"plan"}, {"plan", "--detail", "evidence"}, {"findings"}, {"missing"}, {"commands"}, {"apply"}} {
		for _, depth := range []int{0, 1, 2} {
			t.Run(leaf[0]+strconv.Itoa(depth)+strconv.Itoa(len(leaf)), func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				t.Cleanup(cancel)
				if out, err := exec.CommandContext(ctx, "git", "init", "--quiet", root).CombinedOutput(); err != nil {
					t.Fatalf("git init: %v: %s", err, out)
				}
				if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "root.go"), []byte("package fixture\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				nested := filepath.Join(root, "nested", "source.go")
				if err := os.WriteFile(nested, []byte("package nested\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				var out bytes.Buffer
				args := append([]string{"--repo", root, "--format", "json", "clean"}, leaf...)
				args = append(args, "--max-depth", strconv.Itoa(depth))
				if err := cli.Run(ctx, args, newTestDeps(&out)); err != nil {
					t.Fatalf("run: %v\n%s", err, out.String())
				}
				var envelope struct {
					Analysis analyze.Status             `json:"analysis"`
					Result   map[string]json.RawMessage `json:"result"`
				}
				if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				status := envelope.Analysis
				if status.Complete != (depth == 2) {
					t.Fatalf("depth %d coverage = %+v", depth, status)
				}
				if depth < 2 && (!slices.Contains(status.Limits, "max_depth") || status.SkippedCount == 0) {
					t.Fatalf("missing depth provenance: %+v", status)
				}
				if depth == 1 && !slices.Contains(status.Skipped, "nested/source.go") {
					t.Fatalf("missing actual skipped file: %+v", status)
				}
				if depth == 2 && (len(status.Limits) != 0 || len(status.Skipped) != 0) {
					t.Fatalf("full walk has limits: %+v", status)
				}
				if _, ok := envelope.Result["Coverage"]; ok {
					t.Fatal("internal coverage leaked into result")
				}
				if _, ok := envelope.Result["complete"]; ok {
					t.Fatal("coverage duplicated inside result")
				}
				if leaf[0] == "apply" {
					var confirmed bool
					if err := json.Unmarshal(envelope.Result["confirmed"], &confirmed); err != nil {
						t.Fatal(err)
					}
					if confirmed {
						t.Fatal("default apply confirmed")
					}
					var counts struct{ Executed int }
					if err := json.Unmarshal(envelope.Result["counts"], &counts); err != nil {
						t.Fatal(err)
					}
					if counts.Executed != 0 {
						t.Fatalf("executed = %d", counts.Executed)
					}
					if _, err := os.Stat(nested); err != nil {
						t.Fatalf("dry-run changed fixture: %v", err)
					}
				}
			})
		}
	}
}
