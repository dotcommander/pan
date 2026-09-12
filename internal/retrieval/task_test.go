package retrieval

import (
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

func TestTaskRejectsBudgetTooSmallForRequiredReport(t *testing.T) {
	t.Parallel()

	_, err := Task(analyze.Snapshot{}, nil, "change main", TaskOptions{Tokens: 1})
	if err == nil || !strings.Contains(err.Error(), "cannot encode report schema") {
		t.Fatalf("Task error = %v, want schema budget error", err)
	}
}

func TestTaskReportsAConvergedBoundedTokenCount(t *testing.T) {
	t.Parallel()

	report, err := Task(analyze.Snapshot{}, nil, "change main", TaskOptions{Tokens: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if report.Budget.UsedTokens != taskTokens(report) {
		t.Fatalf("used tokens = %d, encoded tokens = %d", report.Budget.UsedTokens, taskTokens(report))
	}
	if report.Budget.UsedTokens > report.Budget.MaxTokens {
		t.Fatalf("used tokens = %d, max tokens = %d", report.Budget.UsedTokens, report.Budget.MaxTokens)
	}
}

func TestTaskReportsSelectedPolicy(t *testing.T) {
	t.Parallel()

	snap := analyze.Snapshot{
		Root:     t.TempDir(),
		Files:    []analyze.File{{Path: "owner.ts", Language: "typescript"}},
		Captured: map[string][]byte{"owner.ts": []byte("export class Owner {}\n")},
	}
	ranked := ranking.Rank(snap, "", ranking.Options{})
	report, err := Task(snap, ranked, "owner", TaskOptions{Tokens: 1024, PolicyID: StructuralReferenceGraphPolicy})
	if err != nil {
		t.Fatal(err)
	}
	if report.Selection.PolicyID != StructuralReferenceGraphPolicy {
		t.Fatalf("policy = %q", report.Selection.PolicyID)
	}
}
