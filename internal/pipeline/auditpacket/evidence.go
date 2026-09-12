package auditpacket

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/dotcommander/pan/internal/pipeline/storyboard"
)

func buildCommands(sb storyboard.Storyboard, opts Options) []Command {
	out := make([]Command, 0, len(sb.Commands))
	for _, c := range sb.Commands {
		out = append(out, Command{
			Path:           c.Path,
			Short:          c.Short,
			Runnable:       c.Runnable,
			HasSubcommands: c.HasSubcommands,
			HelpProvenance: opts.HelpProvenance,
			ExecutionRisk:  opts.CommandHelpExecuted,
		})
	}
	return out
}

func buildWrites(sb storyboard.Storyboard) []Write {
	out := make([]Write, 0, len(sb.Stores))
	for _, s := range sb.Stores {
		writers := make([]string, 0, len(s.Writers))
		for _, w := range s.Writers {
			writers = append(writers, w.Stage)
		}
		out = append(out, Write{Store: s.Name, Writers: writers})
	}
	return out
}

func buildSubprocesses(sb storyboard.Storyboard, root string, opts Options) []Subprocess {
	var out []Subprocess
	if opts.CommandHelpExecuted {
		out = append(out, Subprocess{
			Command:              "go run <target> --help",
			Source:               "pan:command-help",
			Cwd:                  root,
			TimeoutSeconds:       10,
			TargetControlledArgs: false,
			Evidence:             []string{"internal/pipeline/storyboard/commandhelp.go: runProjectCommandHelp"},
		})
	}
	seen := map[string]bool{}
	for _, ph := range allPhases(sb) {
		for _, src := range ph.Sources {
			if src != "subprocess" || seen[ph.Name] {
				continue
			}
			seen[ph.Name] = true
			out = append(out, Subprocess{
				Command:  "os/exec (scan signal)",
				Source:   "target:scan",
				Evidence: []string{"phase: " + ph.Name},
			})
		}
	}
	out = append(out, staticSubprocesses(root, seen)...)
	return out
}

func staticSubprocesses(root string, seen map[string]bool) []Subprocess {
	var out []Subprocess
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	fset := token.NewFileSet()
	_ = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		// An unreadable entry has no source evidence to collect. WalkDir may
		// provide a nil entry with its error; skip it without dereferencing it.
		if err == nil && d != nil {
			if d.IsDir() {
				switch d.Name() {
				case ".git", "vendor", "node_modules":
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".go" {
				return nil
			}
			out = append(out, staticSubprocessesInFile(fset, absRoot, path, seen)...)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Evidence[0] == out[j].Evidence[0] {
			return out[i].Command < out[j].Command
		}
		return out[i].Evidence[0] < out[j].Evidence[0]
	})
	return out
}

func staticSubprocessesInFile(fset *token.FileSet, root, path string, seen map[string]bool) []Subprocess {
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil
	}
	var out []Subprocess
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		cmd, ok := subprocessCommandFromCall(call)
		if !ok {
			return true
		}
		evidence := callEvidence(fset, root, call)
		key := cmd + "\x00" + evidence
		if seen[key] {
			return true
		}
		seen[key] = true
		out = append(out, Subprocess{
			Command:              cmd,
			Source:               "target:static",
			Evidence:             []string{evidence},
			TargetControlledArgs: true,
		})
		return true
	})
	return out
}

func subprocessCommandFromCall(call *ast.CallExpr) (string, bool) {
	callee, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := callee.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	return subprocessCommand(pkg.Name, callee.Sel.Name, call.Args)
}

func callEvidence(fset *token.FileSet, root string, call *ast.CallExpr) string {
	pos := fset.Position(call.Pos())
	rel, err := filepath.Rel(root, pos.Filename)
	if err != nil {
		rel = pos.Filename
	}
	return filepath.ToSlash(rel) + ":" + strconv.Itoa(pos.Line)
}

func subprocessCommand(pkg, name string, args []ast.Expr) (string, bool) {
	var argIndex int
	switch {
	case pkg == "exec" && name == "Command":
		argIndex = 0
	case pkg == "exec" && name == "CommandContext":
		argIndex = 1
	case pkg == "os" && name == "StartProcess":
		argIndex = 0
	default:
		return "", false
	}
	if len(args) <= argIndex {
		return pkg + "." + name, true
	}
	if lit, ok := args[argIndex].(*ast.BasicLit); ok && lit.Kind == token.STRING {
		if value, err := strconv.Unquote(lit.Value); err == nil && value != "" {
			return value, true
		}
	}
	return pkg + "." + name, true
}

func allPhases(sb storyboard.Storyboard) []storyboard.Phase {
	phases := append([]storyboard.Phase(nil), sb.Prelude...)
	for _, lane := range sb.CommandLanes {
		phases = append(phases, lane.Phases...)
	}
	return phases
}

func buildDrift(sb storyboard.Storyboard) []Drift {
	out := make([]Drift, 0, len(sb.Diffs))
	for _, d := range sb.Diffs {
		out = append(out, Drift{Area: d.Area, Status: d.Status, Detail: d.Detail, Evidence: d.Evidence})
	}
	return out
}
