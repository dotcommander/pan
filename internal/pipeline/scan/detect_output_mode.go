package scan

import (
	"go/ast"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// Output-mode labels returned by detectOutputMode.
const (
	outputModeFile = "file"
	outputModeHTTP = "HTTP"
)

// maxOutputModeDepth bounds how far detectOutputMode follows call chains.
const maxOutputModeDepth = 2

// fnInfo records what kind of output a function produces and which local
// functions it calls.
type fnInfo struct {
	writeFile bool
	http      bool
	callees   []string // functions called from this body
}

// detectOutputMode checks what kind of output a function produces by
// inspecting its body and following call chains up to 2 levels deep.
// Returns "file" for os.WriteFile patterns, "HTTP" for net/http patterns,
// "" otherwise.
func detectOutputMode(root string, phases []spec.Phase, fnName string) string {
	funcs := buildFunctionModes(root, phases)
	visited := make(map[string]bool)
	hasFile, hasHTTP := followOutputModes(funcs, visited, fnName, 0)
	switch {
	case hasFile:
		return outputModeFile
	case hasHTTP:
		return outputModeHTTP
	}
	return ""
}

// buildFunctionModes parses every phase file and records, per function, its
// output signals and direct local callees.
func buildFunctionModes(root string, phases []spec.Phase) map[string]*fnInfo {
	funcs := make(map[string]*fnInfo)
	for _, p := range phases {
		files, _ := parseGoFiles(root, p.Files)
		for _, f := range files {
			collectFunctionModes(f, funcs)
		}
	}
	return funcs
}

// collectFunctionModes fills funcs with one entry per top-level function
// declaration in the file.
func collectFunctionModes(f *ast.File, funcs map[string]*fnInfo) {
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		info := &fnInfo{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if ok {
				collectCallMode(sel, info)
			} else if ident, ok := call.Fun.(*ast.Ident); ok {
				// Same-package callee: runRender, etc.
				info.callees = append(info.callees, ident.Name)
			}
			return true
		})
		funcs[fn.Name.Name] = info
	}
}

// collectCallMode records the output signal of one selector call and tracks
// cross-package callees (serve.Run, render.Render, etc.).
func collectCallMode(sel *ast.SelectorExpr, info *fnInfo) {
	switch sel.Sel.Name {
	case "WriteFile":
		info.writeFile = true
	case "ListenAndServe", "Serve", "ListenAndServeTLS":
		info.http = true
	}
	// Track cross-package callee: serve.Run, render.Render, etc.
	if _, ok := sel.X.(*ast.Ident); ok {
		info.callees = append(info.callees, sel.Sel.Name)
	}
}

// followOutputModes reports whether the named function or any callee within
// the depth bound writes files or serves HTTP.
func followOutputModes(funcs map[string]*fnInfo, visited map[string]bool, name string, depth int) (hasFile, hasHTTP bool) {
	if depth > maxOutputModeDepth || visited[name] {
		return false, false
	}
	visited[name] = true
	info, ok := funcs[name]
	if !ok {
		return false, false
	}
	if info.writeFile {
		hasFile = true
	}
	if info.http {
		hasHTTP = true
	}
	if depth >= maxOutputModeDepth {
		return hasFile, hasHTTP
	}
	for _, callee := range info.callees {
		f, h := followOutputModes(funcs, visited, callee, depth+1)
		if f {
			hasFile = true
		}
		if h {
			hasHTTP = true
		}
	}
	return hasFile, hasHTTP
}
