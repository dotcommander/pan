package storyboard

import (
	"context"
	"errors"
	"fmt"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/ownedprocess"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCommandHelpProcessFixture(t *testing.T) {
	t.Parallel()
	mode := os.Getenv("PAN_HELP_FIXTURE")
	if mode == "" {
		return
	}
	_ = os.WriteFile(os.Getenv("PAN_HELP_READY"), []byte("executed"), 0600)
	switch mode {
	case "deadline":
		<-time.After(time.Hour)
	case "output":
		fmt.Print(strings.Repeat("x", 8192))
	case "total":
		if os.Getenv("PAN_HELP_CHILD") == "true" {
			fmt.Print("Usage:\n  fixture child [flags]\n" + strings.Repeat("x", 80))
		} else {
			fmt.Print("Usage:\n  fixture [command]\n\nAvailable Commands:\n  child       Child command\n")
		}
	}
	os.Exit(0)
}
func TestCommandHelpExecutingLimits(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"invocation", "overall", "output", "total"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			ready := filepath.Join(root, "ready")
			if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main(){}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var children []*exec.Cmd
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			t.Cleanup(cancel)
			ctx = context.WithValue(ctx, commandHelpCommandKey{}, func(args []string) *exec.Cmd {
				fixtureMode := mode
				if mode == "invocation" || mode == "overall" {
					fixtureMode = "deadline"
				}
				cmd := exec.Command(os.Args[0], "-test.run=^TestCommandHelpProcessFixture$")
				cmd.Env = append(os.Environ(), "PAN_HELP_FIXTURE="+fixtureMode, "PAN_HELP_READY="+ready, "GORACE=atexit_sleep_ms=0", fmt.Sprintf("PAN_HELP_CHILD=%t", len(args) > 3))
				mu.Lock()
				children = append(children, cmd)
				mu.Unlock()
				return cmd
			})
			rules := config.Default().CommandHelp
			rules.Timeout = 2 * time.Second
			rules.InvocationTimeout = time.Second
			switch mode {
			case "invocation":
				rules.InvocationTimeout = 500 * time.Millisecond
			case "overall":
				rules.Timeout = 500 * time.Millisecond
			case "output":
				rules.MaxOutputBytes = 64
			case "total":
				rules.MaxTotalOutputBytes = 100
			}
			_, provenance, err := ResolveCommandHelpWithOptions(ctx, "execute", root, rules)
			if err == nil || !strings.Contains(provenance, "incomplete") {
				t.Fatalf("err=%v provenance=%s", err, provenance)
			}
			if mode == "invocation" || mode == "overall" {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("missing deadline: %v", err)
				}
			} else {
				if !errors.Is(err, ownedprocess.ErrOutputLimit) {
					t.Fatalf("missing output overflow: %v", err)
				}
			}
			if _, readyErr := os.ReadFile(ready); readyErr != nil {
				t.Fatalf("project fixture never executed: %v", readyErr)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, cmd := range children {
				if cmd.Process == nil || cmd.ProcessState == nil {
					t.Fatal("execution returned before reaping child")
				}
			}
			if mode == "total" && len(children) != 2 {
				t.Fatalf("total budget did not reach second invocation: %d", len(children))
			}
		})
	}
}
