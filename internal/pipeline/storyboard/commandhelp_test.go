package storyboard

import (
	"strings"
	"testing"
)

func TestGoHelpEnvPolicy(t *testing.T) {
	t.Parallel()
	entries := []string{"PATH=/bin", "HOME=/home/test", "GOWORK=/private", "GOFLAGS=-toolexec=secret", "OPENAI_API_KEY=secret", "HTTPS_PROXY=https://user:secret@example.com", "GOPROXY=https://proxy.example,direct", "NO_PROXY=localhost,10.0.0.0/8", "TMPDIR=bad\nvalue"}
	got := strings.Join(commandHelpEnvironment(entries, "linux"), "\n")
	for _, want := range []string{"GOENV=off", "GOWORK=off", "GOPROXY=https://proxy.example,direct", "NO_PROXY=localhost,10.0.0.0/8"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s: %s", want, got)
		}
	}
	for _, forbidden := range []string{"secret", "GOFLAGS", "HTTPS_PROXY", "TMPDIR"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("retained %s", forbidden)
		}
	}
}
func TestHelpWindowsEnvironment(t *testing.T) {
	t.Parallel()
	got := strings.Join(commandHelpEnvironment([]string{"Path=first", "PATH=last", "SystemRoot=C:\\Windows", "Http_Proxy=https://proxy.example"}, "windows"), "\n")
	if !strings.Contains(got, "PATH=last") || strings.Contains(got, "first") || !strings.Contains(got, "SYSTEMROOT=") {
		t.Fatalf("unexpected environment: %s", got)
	}
}
func TestHelpNetworkValidation(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"https://user:pass@host", "https://host?q=secret", "https://host#secret", "https:///missing", "javascript://host"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			got := commandHelpEnvironment([]string{"GOPROXY=" + value}, "linux")
			if strings.Contains(strings.Join(got, "\n"), "GOPROXY=") {
				t.Fatal("unsafe endpoint admitted")
			}
		})
	}
}

func TestHelpRootName(t *testing.T) {
	t.Parallel()
	help := `Usage:
  clickmojo backtest [flags]
  clickmojo backtest [command]
`
	if got := helpRootName(help); got != "clickmojo" {
		t.Fatalf("helpRootName() = %q, want clickmojo", got)
	}
}

func TestHelpRunnableCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		help string
		want bool
	}{
		{
			name: "command group",
			help: `Usage:
  clickmojo calibrate [command]
`,
			want: false,
		},
		{
			name: "runnable command with child commands",
			help: `Usage:
  clickmojo backtest [flags]
  clickmojo backtest [command]
`,
			want: true,
		},
		{
			name: "leaf command",
			help: `Usage:
  clickmojo calibrate bids [flags]
`,
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := helpRunnableCommand(tt.help); got != tt.want {
				t.Fatalf("helpRunnableCommand() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHelpCommandSignatureMatchesAliases(t *testing.T) {
	t.Parallel()
	backtestCalibrate := `Sweep conservative bid policy settings against backtest data

Usage:
  clickmojo backtest calibrate [flags]

Flags:
      --discount string
  -h, --help
      --max-overpay float
`
	calibrateBids := `Sweep conservative bid policy settings against backtest data

Usage:
  clickmojo calibrate bids [flags]

Flags:
      --discount string
  -h, --help
      --max-overpay float
`
	if helpCommandSignature(backtestCalibrate) != helpCommandSignature(calibrateBids) {
		t.Fatalf("equivalent command signatures differ: %q vs %q", helpCommandSignature(backtestCalibrate), helpCommandSignature(calibrateBids))
	}
}

func TestHelpAvailableCommands(t *testing.T) {
	t.Parallel()
	help := `Available Commands:
  audit-features     Report non-null coverage
  calibrate-value    Fit monotone fair-value calibration from backtest residuals
  residuals          Analyze prediction residuals

Flags:
  -h, --help   help for model
`
	got := helpAvailableCommands(help)
	want := []string{"audit-features", "calibrate-value", "residuals"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("helpAvailableCommands() = %#v, want %#v", got, want)
	}
}

func TestFlatHelpSubcommands(t *testing.T) {
	t.Parallel()
	help := `Usage: soma [flags] [prompt --json]

Flags:
  --json             Run one prompt and print a JSON timing/usage report

Subcommands:
  version            Print version and exit
  update             Re-install the latest release via go install
`
	if !helpUsesFlatSubcommands(help) {
		t.Fatal("helpUsesFlatSubcommands() = false, want true")
	}
	got := helpCommandRows(help)
	if len(got) != 2 {
		t.Fatalf("helpCommandRows() len = %d, want 2: %#v", len(got), got)
	}
	if got[0].Name != "version" || got[0].Description != "Print version and exit" {
		t.Fatalf("first row = %#v", got[0])
	}
	if root := helpRootName(help); root != "soma" {
		t.Fatalf("helpRootName() = %q, want soma", root)
	}
}
