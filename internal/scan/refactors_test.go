package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestRefactorSignaturesGroupsExactNormalizedBodies(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	body := "{\n\ta := 1\n\tb := 2\n\tc := 3\n\td := 4\n\te := 5\n\tf := 6\n\tg := 7\n\t_ = a + b + c + d + e + f + g\n\treturn\n}"
	source := "package sample\nfunc one() " + body + "\nfunc two() " + body + "\n"
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := RefactorSignatures(context.Background(), analyze.Snapshot{Root: root, Files: []analyze.File{{Path: "sample.go", Language: languageGo}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Groups) != 1 || len(report.Groups[0].Sites) != 2 {
		t.Fatalf("duplicate groups = %#v", report.Groups)
	}
}
