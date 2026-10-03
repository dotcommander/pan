package storyboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/ownedprocess"
	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
	"github.com/dotcommander/pan/internal/pipeline/scan"
	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// ResolveCommandHelp returns the target's command surface and a provenance token
// for the requested mode. "execute" is the only mode that compiles and runs the
// target binary (go run <target> --help); "off" and "static" never execute
// target code.
func ResolveCommandHelp(ctx context.Context, mode, root string) ([]Command, string, error) {
	return ResolveCommandHelpWithOptions(ctx, mode, root, config.CommandHelpRules{})
}

func ResolveCommandHelpWithOptions(ctx context.Context, mode, root string, rules config.CommandHelpRules) ([]Command, string, error) {
	rules = rules.Normalized()
	switch mode {
	case "off":
		return nil, "skipped (off)", nil
	case "static":
		return nil, "static-unsupported", nil
	case "execute":
		commands, err := collectProjectCommandHelpOptions(ctx, root, rules)
		if err != nil {
			return commands, "incomplete (execution failed)", err
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
	return collectProjectCommandHelpOptions(ctx, root, config.CommandHelpRules{})
}
func collectProjectCommandHelpOptions(ctx context.Context, root string, rules config.CommandHelpRules) ([]Command, error) {
	rules = rules.Normalized()
	if rules.Timeout < 0 || rules.InvocationTimeout < 0 || rules.MaxDepth < 0 || rules.MaxInvocations < 0 || rules.MaxOutputBytes < 0 || rules.MaxTotalOutputBytes < 0 {
		return nil, fmt.Errorf("negative command-help limit")
	}
	ctx, cancel := context.WithTimeout(ctx, rules.Timeout)
	defer cancel()
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	entry, ok := projectCommandEntry(absRoot)
	if !ok {
		return nil, nil
	}

	collector := commandHelpCollector{ctx: ctx, root: absRoot, entry: entry, seen: map[string]bool{}, rules: rules}
	if err := collector.collect(nil); err != nil {
		return collector.commands, err
	}
	sort.SliceStable(collector.commands, func(i, j int) bool { return collector.commands[i].Path < collector.commands[j].Path })
	return collector.commands, nil
}

type commandHelpCollector struct {
	rules       config.CommandHelpRules
	invocations int
	totalOutput int
	ctx         context.Context
	root        string
	entry       string
	rootName    string
	seen        map[string]bool
	commands    []Command
}

func (c *commandHelpCollector) collect(args []string) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if len(args) > c.rules.MaxDepth {
		return fmt.Errorf("command-help depth limit exceeded")
	}
	if c.invocations >= c.rules.MaxInvocations {
		return fmt.Errorf("command-help invocation limit exceeded")
	}
	if c.seen[strings.Join(args, "\x00")] {
		return nil
	}
	c.seen[strings.Join(args, "\x00")] = true
	c.invocations++
	remaining := c.rules.MaxTotalOutputBytes - c.totalOutput
	if remaining <= 0 {
		return fmt.Errorf("command-help total output limit exceeded")
	}
	limit := min(remaining, c.rules.MaxOutputBytes)
	help, err := runProjectCommandHelpOptions(c.ctx, c.root, c.entry, args, c.rules.InvocationTimeout, limit)
	c.totalOutput += len(help)
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
	rules := config.Default().CommandHelp
	return runProjectCommandHelpOptions(ctx, root, entry, args, rules.InvocationTimeout, rules.MaxOutputBytes)
}
func runProjectCommandHelpOptions(ctx context.Context, root, entry string, args []string, timeout time.Duration, limit int) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
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
	if factory, ok := ctx.Value(commandHelpCommandKey{}).(func([]string) *exec.Cmd); ok {
		cmd = factory(cmdArgs)
		cmd.Dir = root
	}
	result, err := ownedprocess.Run(runCtx, cmd, ownedprocess.Limits{CombinedBytes: limit})
	out := append(result.Stdout, result.Stderr...)
	if err != nil {
		return "", fmt.Errorf("go %s: %w\n%s", strings.Join(cmdArgs, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func combinedOutputContext(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	rules := config.Default().CommandHelp
	result, err := ownedprocess.Run(ctx, cmd, ownedprocess.Limits{CombinedBytes: rules.MaxOutputBytes})
	return append(result.Stdout, result.Stderr...), err
}

func goHelpEnv() []string { return commandHelpEnvironment(os.Environ(), runtime.GOOS) }

// commandHelpCommandKey scopes executable fixtures to one help traversal.
type commandHelpCommandKey struct{}
