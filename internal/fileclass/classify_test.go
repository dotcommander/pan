package fileclass

import "testing"

func TestClassify(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		path      string
		language  string
		generated bool
		want      Class
	}{
		{"generated wins", "vendor/x_test.go", "go", true, Generated},
		{"generated directory", "generated/client.go", "go", false, Generated},
		{"vendor", "vendor/example.go", "go", false, Vendor},
		{"node modules", "node_modules/pkg/index.js", "javascript", false, Vendor},
		{"go test", "internal/app/app_test.go", "go", false, Test},
		{"test directory", "tests/case.go", "go", false, Test},
		{"root fixture", "testdata/parity/main.go", "go", false, Fixture},
		{"nested fixture windows", `internal\app\testdata\case.json`, "json", false, Fixture},
		{"docs", "docs/cli.md", "markdown", false, Docs},
		{"root readme", "README.md", "markdown", false, Docs},
		{"example", "examples/basic/main.go", "go", false, Example},
		{"work receipt", ".work/audit/report.json", "json", false, Data},
		{"jsonl receipt", "logs/events.jsonl", "json", false, Data},
		{"production", "internal/app/app.go", "go", false, Production},
		{"unknown", "config/schema.json", "unknown", false, Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Classify(tt.path, tt.language, tt.generated); got != tt.want {
				t.Fatalf("Classify(%q, %q, %v) = %q, want %q", tt.path, tt.language, tt.generated, got, tt.want)
			}
		})
	}
}
