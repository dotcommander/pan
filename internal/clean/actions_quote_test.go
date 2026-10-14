package clean

import (
	"slices"
	"testing"
)

func TestShellQuotePreservesWhitespaceWords(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ word, want string }{
		{"", "''"},
		{"ordinary.tmp", "ordinary.tmp"},
		{"two words.tmp", "'two words.tmp'"},
		{"operator's notes.tmp", "'operator'\\''s notes.tmp'"},
		{"line\nbreak.tmp", "'line\nbreak.tmp'"},
		{"carriage\rreturn.tmp", "'carriage\rreturn.tmp'"},
		{"tab\tname.tmp", "'tab\tname.tmp'"},
	} {
		t.Run(tc.word, func(t *testing.T) {
			t.Parallel()
			if got := shellQuote(tc.word); got != tc.want {
				t.Fatalf("quote = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildActionsQuotesNewlineWithoutChangingArgv(t *testing.T) {
	t.Parallel()
	filename := "line\nbreak.tmp"
	actions := BuildActions(Plan{DeleteCandidates: []Candidate{{File: filename}}}, testOptions(t.TempDir()))
	if len(actions) != 1 {
		t.Fatalf("actions = %+v", actions)
	}
	action := actions[0]
	if !slices.Equal(action.Argv, []string{"rm", "-f", "--", filename}) || action.Target != filename {
		t.Fatalf("filename identity changed: %+v", action)
	}
	if action.Display != "rm -f -- 'line\nbreak.tmp'" {
		t.Fatalf("display = %q", action.Display)
	}
}
