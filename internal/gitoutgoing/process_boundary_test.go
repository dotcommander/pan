package gitoutgoing

import (
	"bufio"
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

func outgoingFixture(mode, command string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestOutgoingProcessFixture$")
	cmd.Env = append(os.Environ(), "PAN_OUTGOING_FIXTURE="+mode, "PAN_OUTGOING_COMMAND="+command, "GORACE=atexit_sleep_ms=0")
	return cmd
}
func TestOutgoingProcessFixture(t *testing.T) {
	t.Parallel()
	mode := os.Getenv("PAN_OUTGOING_FIXTURE")
	if mode == "" {
		return
	}
	if mode == "hold" {
		<-time.After(time.Hour)
		os.Exit(0)
	}
	command := os.Getenv("PAN_OUTGOING_COMMAND")
	if command == "log" {
		switch mode {
		case "overflow":
			fmt.Print(strings.Repeat("x", 8192))
			os.Exit(0)
		case "descendant":
			cmd := outgoingFixture("hold", "")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				os.Exit(2)
			}
		}
		fmt.Printf("\x00%s\x00\n:000000 100644 %s %s A\x00fixture.go\x00", strings.Repeat("a", 40), strings.Repeat("0", 40), strings.Repeat("b", 40))
		os.Exit(0)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		switch mode {
		case "cancel":
			_ = os.WriteFile(os.Getenv("PAN_OUTGOING_READY"), []byte("entered batch request"), 0600)
			<-time.After(time.Hour)
		case "malformed":
			fmt.Println("not-an-object blob -1")
		case "header":
			fmt.Println(strings.Repeat("x", 8192))
		default:
			fmt.Printf("%s blob 1\n", scanner.Text())
		}
	}
	os.Exit(0)
}
func TestOutgoingInspectionProcessFailuresReap(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"cancel", "malformed", "header", "overflow", "descendant"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			t.Cleanup(cancel)
			root := t.TempDir()
			ready := filepath.Join(root, "ready")
			var mu sync.Mutex
			var commands []*exec.Cmd
			ctx = context.WithValue(ctx, commandFactoryKey{}, func(args ...string) *exec.Cmd {
				cmd := outgoingFixture(mode, args[0])
				cmd.Env = append(cmd.Env, "PAN_OUTGOING_READY="+ready)
				mu.Lock()
				commands = append(commands, cmd)
				mu.Unlock()
				return cmd
			})
			rules := config.Default().OutgoingGit
			if mode == "cancel" {
				rules.Timeout = 500 * time.Millisecond
			}
			if mode == "overflow" {
				rules.MaxLogBytes = 64
			}
			if mode == "header" {
				rules.MaxHeaderBytes = 64
			}
			report, err := InspectContext(ctx, "HEAD", root, 1048576, rules)
			if err == nil {
				t.Fatalf("failed inspection returned clean evidence: %+v", report)
			}
			if mode == "cancel" {
				if _, readyErr := os.ReadFile(ready); readyErr != nil {
					t.Fatalf("batch never reached blocked request: %v", readyErr)
				}
			}
			if mode == "cancel" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("missing active deadline: %v", err)
			}
			if mode == "overflow" && !errors.Is(err, ownedprocess.ErrOutputLimit) {
				t.Fatalf("missing overflow: %v", err)
			}
			if mode == "descendant" && !errors.Is(err, exec.ErrWaitDelay) {
				t.Fatalf("missing held-pipe failure: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(commands) == 0 {
				t.Fatal("fixture was not executed")
			}
			for _, cmd := range commands {
				if cmd.Process == nil || cmd.ProcessState == nil {
					t.Fatal("inspection returned without reaping owned subprocess")
				}
			}
		})
	}
}
