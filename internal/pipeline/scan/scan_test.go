package scan

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanZeroConfigUsesDefaultCaps(t *testing.T) {
	root := t.TempDir()
	initScanFixtureGit(t, root)
	writeScanFixture(t, root, "go.mod", "module example.com/defaults\n\ngo 1.26\n")
	for i := 0; i < DefaultMaxPhases+3; i++ {
		pkg := fmt.Sprintf("pkg%02d", i)
		writeScanFixture(t, root, filepath.Join("internal", pkg, pkg+".go"), fmt.Sprintf(`package %s

func Do%02d() {}
`, pkg, i))
	}

	zero, err := Scan(context.Background(), root, Config{})
	if err != nil {
		t.Fatalf("Scan zero config: %v", err)
	}
	explicit, err := Scan(context.Background(), root, Config{
		MaxPhases: DefaultMaxPhases,
		MaxStages: DefaultMaxStages,
	})
	if err != nil {
		t.Fatalf("Scan explicit defaults: %v", err)
	}

	if len(zero.Phases) != len(explicit.Phases) {
		t.Fatalf("zero config phase count = %d, explicit defaults = %d", len(zero.Phases), len(explicit.Phases))
	}
	if len(zero.Phases) > DefaultMaxPhases {
		t.Fatalf("zero config phase count = %d, want at most %d", len(zero.Phases), DefaultMaxPhases)
	}
	for i := range zero.Phases {
		if zero.Phases[i].Name != explicit.Phases[i].Name {
			t.Fatalf("phase %d name = %q, explicit defaults = %q", i, zero.Phases[i].Name, explicit.Phases[i].Name)
		}
		if len(zero.Phases[i].Stages) != len(explicit.Phases[i].Stages) {
			t.Fatalf("phase %d stage count = %d, explicit defaults = %d", i, len(zero.Phases[i].Stages), len(explicit.Phases[i].Stages))
		}
		if len(zero.Phases[i].Stages) > DefaultMaxStages {
			t.Fatalf("phase %d stage count = %d, want at most %d", i, len(zero.Phases[i].Stages), DefaultMaxStages)
		}
	}
}

func TestValidateConfigRejectsNegativeSizing(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "max phases",
			cfg:  Config{MaxPhases: -1},
			want: "max phases must be non-negative",
		},
		{
			name: "max stages",
			cfg:  Config{MaxStages: -1},
			want: "max stages must be non-negative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(tt.cfg)
			if err == nil {
				t.Fatalf("ValidateConfig error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateConfig error = %v, want %q", err, tt.want)
			}
		})
	}
}

func initScanFixtureGit(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

func writeScanFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
