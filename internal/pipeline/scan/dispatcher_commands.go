package scan

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// readModulePath reads the module directive from go.mod in root.
// Returns ("", false) if go.mod is absent or unparseable.
func readModulePath(root string) (string, bool) {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", false
	}
	defer f.Close() //nolint:errcheck

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), true
		}
	}
	return "", false
}

// findCommandFiles scans dir for *.go files that contain a newXCmd/NewXCmd constructor.
// Returns map[commandName]relativeFilePath relative to the repo root.
func findCommandFiles(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	// dir is an absolute path; we need relative path from the repo root.
	// We derive root by going up two levels from dir (dir = root/internal/commands).
	root := filepath.Dir(filepath.Dir(dir))

	result := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}

		absPath := filepath.Join(dir, e.Name())
		if !fileHasNewCmdConstructor(absPath) {
			continue
		}

		stem := strings.TrimSuffix(e.Name(), ".go")
		// Strip common suffixes and prefixes to derive a clean command name.
		// root.go → "root" (skip — this is the dispatch coordinator, not a leaf command)
		if stem == "root" {
			continue
		}
		rel, err := filepath.Rel(root, absPath)
		if err != nil {
			continue
		}
		result[stem] = rel
	}
	return result, nil
}

// findCobraCommandsInCmd detects Cobra leaf commands declared in a flat cmd/
// package (e.g. `var fooCmd = &cobra.Command{Use: "foo"}`) and maps command
// name → repo-relative file path. Used as a fallback when the
// internal/commands/ + newXCmd() shape is absent. Only top-level cmd/*.go files
// are scanned; the root command (Use first token == binary name, or named
// "root") is skipped. The command name is the first token of the Use field.
func findCobraCommandsInCmd(root, modulePath string) map[string]string {
	cmdDir := filepath.Join(root, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return nil
	}
	binary := filepath.Base(modulePath)
	fset := token.NewFileSet()
	result := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		abs := filepath.Join(cmdDir, e.Name())
		f, perr := parser.ParseFile(fset, abs, nil, 0)
		if perr != nil {
			continue
		}
		rel := relPath(root, abs)
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			recordCobraUseCommand(lit, binary, rel, result)
			return true
		})
	}
	return result
}

// recordCobraUseCommand maps one cobra.Command composite literal to its Use
// command name when it declares a leaf command: the literal's type must be
// cobra.Command, its Use field must be a non-empty string, and the first Use
// token must not be the binary name or the root command. The first file to
// claim a name wins.
func recordCobraUseCommand(lit *ast.CompositeLit, binary, rel string, result map[string]string) {
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok {
		return
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok || pkgIdent.Name != "cobra" || sel.Sel.Name != "Command" {
		return
	}
	use := compositeLitStringField(lit, "Use")
	if use == "" {
		return
	}
	fields := strings.Fields(use)
	if len(fields) == 0 {
		return
	}
	name := fields[0]
	if strings.EqualFold(name, binary) || name == "root" {
		return
	}
	if _, exists := result[name]; !exists {
		result[name] = rel
	}
}

// compositeLitStringField returns the string value of a named field in a
// composite literal (e.g. the Use: "..." field of a cobra.Command), or "" when
// absent or non-string.
func compositeLitStringField(lit *ast.CompositeLit, field string) string {
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != field {
			continue
		}
		bl, ok := kv.Value.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			continue
		}
		return strings.Trim(bl.Value, "`\"")
	}
	return ""
}

// fileHasNewCmdConstructor reports whether the file at path contains a Cobra command constructor.
func fileHasNewCmdConstructor(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close() //nolint:errcheck

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if newCmdPattern.MatchString(scanner.Text()) {
			return true
		}
	}
	return false
}
