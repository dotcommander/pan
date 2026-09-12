package storyboard

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
	"github.com/dotcommander/pan/internal/pipeline/scan"
	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// ResolveCommandHelp returns the target's command surface and a provenance token
// for the requested mode. "execute" is the only mode that compiles and runs the
// target binary (go run <target> --help); "off" and "static" never execute
// target code.
func ResolveCommandHelp(ctx context.Context, mode, root string) ([]Command, string, error) {
	switch mode {
	case "off":
		return nil, "skipped (off)", nil
	case "static":
		return nil, "static-unsupported", nil
	case "execute":
		commands, err := collectProjectCommandHelp(ctx, root)
		if err != nil {
			return nil, "failed", err
		}
		if len(commands) == 0 {
			return nil, "skipped (no command entry)", nil
		}
		return commands, "executed", nil
	default:
		return nil, "", fmt.Errorf("unsupported --command-help %q: use off, execute, or static", mode)
	}
}

// RefreshResult describes a stale-spec refresh attempt.
type RefreshResult struct {
	Path      string
	Refreshed bool
	Reason    string
}

// RefreshStaleSpec refreshes the user-data scan spec for root when it is missing
// or older than staleAfter. A non-positive staleAfter disables refresh.
func RefreshStaleSpec(ctx context.Context, root string, maxPhases, maxStages int, staleAfter time.Duration) (RefreshResult, error) {
	if staleAfter <= 0 {
		return RefreshResult{}, nil
	}
	output, err := spec.ScanOutputPath(root)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("resolve output path: %w", err)
	}
	return refreshSpecAt(ctx, root, output, scan.Config{MaxPhases: maxPhases, MaxStages: maxStages}, staleAfter)
}

func refreshSpecAt(ctx context.Context, root, output string, config scan.Config, staleAfter time.Duration) (RefreshResult, error) {
	link, linkErr := os.Lstat(output)
	if linkErr == nil && link.Mode()&os.ModeSymlink != 0 {
		return RefreshResult{Path: output}, fmt.Errorf("refuse to refresh symlinked spec: %s", output)
	}
	if linkErr != nil && !os.IsNotExist(linkErr) {
		return RefreshResult{Path: output}, fmt.Errorf("lstat %s: %w", output, linkErr)
	}
	info, statErr := os.Stat(output)
	reason := statusMissing
	if statErr == nil {
		age := time.Since(info.ModTime())
		if age < staleAfter {
			return RefreshResult{Path: output}, nil
		}
		reason = "older than " + staleAfter.String()
	} else if !os.IsNotExist(statErr) {
		return RefreshResult{Path: output}, fmt.Errorf("stat %s: %w", output, statErr)
	}

	scanned, err := scan.Scan(ctx, root, config)
	if err != nil {
		return RefreshResult{Path: output}, err
	}
	if statErr == nil {
		existing, loadErr := spec.Load(output)
		if loadErr != nil {
			return RefreshResult{Path: output}, fmt.Errorf("load existing spec: %w", loadErr)
		}
		scanned = scan.Merge(existing, scanned)
	}
	scanned.Root, err = filepath.Abs(root)
	if err != nil {
		return RefreshResult{Path: output}, fmt.Errorf("resolve source root: %w", err)
	}
	data, err := yaml.Marshal(scanned)
	if err != nil {
		return RefreshResult{Path: output}, fmt.Errorf("marshal: %w", err)
	}
	if err := atomicfile.Write(output, data, 0o644); err != nil {
		return RefreshResult{Path: output}, fmt.Errorf("write %s: %w", output, err)
	}
	return RefreshResult{Path: output, Refreshed: true, Reason: reason}, nil
}

func collectProjectCommandHelp(ctx context.Context, root string) ([]Command, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	entry, ok := projectCommandEntry(absRoot)
	if !ok {
		return nil, nil
	}

	collector := commandHelpCollector{ctx: ctx, root: absRoot, entry: entry, seen: map[string]bool{}}
	if err := collector.collect(nil); err != nil {
		return nil, err
	}
	sort.SliceStable(collector.commands, func(i, j int) bool { return collector.commands[i].Path < collector.commands[j].Path })
	return collector.commands, nil
}

type commandHelpCollector struct {
	ctx      context.Context
	root     string
	entry    string
	rootName string
	seen     map[string]bool
	commands []Command
}

func (c *commandHelpCollector) collect(args []string) error {
	if c.seen[strings.Join(args, "\x00")] {
		return nil
	}
	c.seen[strings.Join(args, "\x00")] = true
	help, err := runProjectCommandHelp(c.ctx, c.root, c.entry, args)
	if err != nil {
		return err
	}
	c.setRootName(help)
	children := helpAvailableCommands(help)
	c.commands = append(c.commands, c.command(args, help, len(children) > 0))
	if len(args) == 0 && helpUsesFlatSubcommands(help) {
		c.addFlatCommands(helpCommandRows(help))
		return nil
	}
	return c.collectChildren(args, children)
}

func (c *commandHelpCollector) setRootName(help string) {
	if c.rootName == "" {
		c.rootName = helpRootName(help)
		if c.rootName == "" {
			c.rootName = c.entry
		}
	}
}

func (c *commandHelpCollector) command(args []string, help string, hasSubcommands bool) Command {
	return Command{Path: strings.Join(append([]string{c.rootName}, args...), " "), Short: helpSummary(help), Runnable: helpRunnableCommand(help), HasSubcommands: hasSubcommands, Signature: helpCommandSignature(help)}
}

func (c *commandHelpCollector) addFlatCommands(rows []helpCommandRow) {
	for _, child := range rows {
		if child.Name == commandHelpHelp || child.Name == commandHelpCompletion {
			continue
		}
		c.commands = append(c.commands, Command{Path: c.rootName + " " + child.Name, Short: child.Description, Runnable: true, Signature: child.Description})
	}
}

func (c *commandHelpCollector) collectChildren(args, children []string) error {
	for _, child := range children {
		if child == commandHelpHelp || child == commandHelpCompletion {
			continue
		}
		if err := c.collect(append(append([]string(nil), args...), child)); err != nil {
			return err
		}
	}
	return nil
}

func projectCommandEntry(root string) (string, bool) {
	if rootHasMainPackage(root) {
		return ".", true
	}
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		return "", false
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	if len(dirs) != 1 {
		return "", false
	}
	return dirs[0], true
}

func rootHasMainPackage(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "\npackage main\n") || strings.HasPrefix(string(data), "package main\n") {
			return true
		}
	}
	return false
}

func runProjectCommandHelp(ctx context.Context, root, entry string, args []string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	runTarget := "./cmd/" + entry
	if entry == "." {
		runTarget = "."
	}
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("find go executable: %w", err)
	}
	cmdArgs := append([]string{goRunSubcommand, runTarget}, args...)
	cmdArgs = append(cmdArgs, "--help")
	cmd := &exec.Cmd{
		Path: goExecutable,
		Args: append([]string{goExecutable}, cmdArgs...),
		Dir:  root,
		Env:  goHelpEnv(),
	}
	out, err := combinedOutputContext(runCtx, cmd)
	if err != nil {
		return "", fmt.Errorf("go %s: %w\n%s", strings.Join(cmdArgs, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func combinedOutputContext(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		return output.Bytes(), err
	}
	wait := make(chan error, 1)
	go func() {
		wait <- cmd.Wait()
	}()
	select {
	case err := <-wait:
		return output.Bytes(), err
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-wait
		return output.Bytes(), ctx.Err()
	}
}

func goHelpEnv() []string {
	return append(os.Environ(), "GOWORK=off")
}
