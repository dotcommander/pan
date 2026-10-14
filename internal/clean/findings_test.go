package clean

import "testing"

func TestEmitInventoryFindingsRespectsEntryKind(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		file      FileInfo
		untracked bool
		empty     bool
	}{
		{name: "directory has no index state", file: FileInfo{IsDir: true}},
		{name: "empty directory keeps its signal", file: FileInfo{IsDir: true, IsEmpty: true}, empty: true},
		{name: "untracked file", file: FileInfo{}, untracked: true},
		{name: "tracked file", file: FileInfo{Tracked: true}},
		{name: "unknown git state", file: FileInfo{GitStateUnknown: true}},
		{name: "untracked symlink", file: FileInfo{IsSymlink: true}, untracked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := []FileInfo{tc.file}
			EmitFindings(files, 0)
			got := files[0]
			if got.HasFinding(RuleUntracked) != tc.untracked || got.HasFinding(RuleEmpty) != tc.empty {
				t.Fatalf("findings = %+v; want untracked=%v empty=%v", got.Findings, tc.untracked, tc.empty)
			}
			if !tc.untracked && !tc.empty && len(got.Findings) != 0 {
				t.Fatalf("unexpected signals = %+v", got.Findings)
			}
		})
	}
}

func TestGitEnrichmentDoesNotInventUntrackedDirectoryFindings(t *testing.T) {
	t.Parallel()
	files := []FileInfo{
		{RelPath: "cmd", IsDir: true},
		{RelPath: "cmd/demo", IsDir: true},
		{RelPath: "cmd/demo/main.go"},
	}
	info := gitInfo{available: true, tracked: map[string]bool{"cmd/demo/main.go": true}}
	info.mark(files, Options{})
	EmitFindings(files, 0)
	for _, file := range files {
		if file.HasFinding(RuleUntracked) || Score(&file) != 0 {
			t.Fatalf("tracked source tree has false signal: %+v", file)
		}
	}
}
