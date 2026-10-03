package config

import "testing"

func TestProcessDefaultsNormalize(t *testing.T) {
	t.Parallel()
	cfg, err := Decode([]byte("max_files: 10\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OutgoingGit != Default().OutgoingGit || cfg.CommandHelp != Default().CommandHelp || cfg.Lsp.MaxFrameBytes != 33554432 || cfg.Improve.MaxStdoutBytes != 1048576 {
		t.Fatal("process defaults missing")
	}
}
func TestNegativeProcessLimitsRejected(t *testing.T) {
	t.Parallel()
	for _, data := range []string{"lsp:\n  max_frame_bytes: -1\n", "improve:\n  max_stdout_bytes: -1\n", "outgoing_git:\n  max_log_bytes: -1\n", "command_help:\n  max_invocations: -1\n"} {
		t.Run(data, func(t *testing.T) {
			t.Parallel()
			if _, err := Decode([]byte(data)); err == nil {
				t.Fatal("negative limit accepted")
			}
		})
	}
}
