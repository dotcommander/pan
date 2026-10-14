package scan

import (
	"slices"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestInventoryEvidenceUsesCapturedCall(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		effect Effect
		op     string
		role   string
		ok     bool
	}{
		{name: "structured reader", effect: Effect{Op: "os.ReadFile", Evidence: "parsed call expression"}, op: "os.ReadFile", role: roleReader, ok: true},
		{name: "structured writer", effect: Effect{Op: "os.MkdirAll", Evidence: "parsed call expression"}, op: "os.MkdirAll", role: roleWriter, ok: true},
		{name: "structured constructor", effect: Effect{Op: "sql.Open", Evidence: "parsed call expression"}, op: "sql.Open", role: roleConstructor, ok: true},
		{name: "identity beats display", effect: Effect{Op: "os.ReadFile", Evidence: "os.WriteFile(path, data, mode)"}, op: "os.ReadFile", role: roleReader, ok: true},
		{name: "lexical fallback", effect: Effect{Evidence: "os.WriteFile(path, data, mode)"}, op: "os.WriteFile", role: roleWriter, ok: true},
		{name: "unknown identity does not use display", effect: Effect{Op: "client.Unknown", Evidence: "os.WriteFile(path, data, mode)"}, op: "client.Unknown"},
		{name: "no call", effect: Effect{Evidence: "parsed call expression"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			roles, op, ok := classifyInventoryEvidence(tc.effect, inventoryRoles())
			if ok != tc.ok || op != tc.op || (tc.ok && !slices.Equal(roles, []string{tc.role})) {
				t.Fatalf("classification = %v, %q, %v; want role=%q op=%q ok=%v", roles, op, ok, tc.role, tc.op, tc.ok)
			}
		})
	}
}

func TestInventoryFindsStructuredFilesystemOwners(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"reader.go":      "package files\nimport \"os\"\nfunc Read() { os.ReadFile(\"input\") }\n",
		"writer.go":      "package files\nimport \"os\"\nfunc Write() { os.WriteFile(\"output\", nil, 0o600) }\n",
		"writer_test.go": "package files\nimport \"os\"\nfunc OnlyTest() { os.Remove(\"output\") }\n",
	})
	snap := goSnapshot(dir, "reader.go", "writer.go", "writer_test.go")
	report, err := Inventory(t.Context(), snap, "filesystem", 0)
	if err != nil {
		t.Fatal(err)
	}
	if report.Boundary != boundaryFilesystem || len(report.Owners) != 2 {
		t.Fatalf("filesystem owners = %+v", report)
	}
	for i, want := range []struct{ path, role, op string }{
		{"reader.go", roleReader, "os.ReadFile"},
		{"writer.go", roleWriter, "os.WriteFile"},
	} {
		got := report.Owners[i]
		if got.Path != want.path || !slices.Equal(got.Roles, []string{want.role}) || len(got.Evidence) != 1 {
			t.Fatalf("owner %d = %+v; want %+v", i, got, want)
		}
		evidence := got.Evidence[0]
		if evidence.Op != want.op || evidence.Role != want.role || evidence.Line != 3 || evidence.Confidence != analyze.ConfidenceLexical {
			t.Fatalf("evidence = %+v; want %+v on line 3", evidence, want)
		}
	}
	capped, err := Inventory(t.Context(), snap, "fs", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped.Owners) != 1 || capped.Owners[0].Path != "reader.go" || len(capped.Truncations) != 1 ||
		capped.Truncations[0].Field != "owners" || capped.Truncations[0].Shown != 1 || capped.Truncations[0].Total != 2 {
		t.Fatalf("owner cap = %+v", capped)
	}
}
