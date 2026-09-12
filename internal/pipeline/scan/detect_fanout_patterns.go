package scan

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// addCommandFanout detects Cobra AddCommand clusters in a function body.
// Returns a Fanout if 2+ AddCommand calls are found, else nil.
func addCommandFanout(_ *token.FileSet, body *ast.BlockStmt) *spec.Fanout {
	var targets []spec.Target
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "AddCommand" {
			return true
		}
		for _, arg := range call.Args {
			label := argLabel(arg)
			flag := deriveFlag(label)
			targets = append(targets, spec.Target{Label: label, Flag: flag})
		}
		return true
	})
	if len(targets) < 2 {
		return nil
	}
	return &spec.Fanout{Gate: gateSubcommand, Targets: targets}
}

// goroutineFanout detects 2+ goroutine launches in a function body.
// Returns a Fanout if 2+ go stmts are found, else nil.
// Goroutines that launch blocked stdlib calls (e.g. go log.Fatal) are skipped.
func goroutineFanout(body *ast.BlockStmt) *spec.Fanout {
	var targets []spec.Target
	ast.Inspect(body, func(n ast.Node) bool {
		gs, ok := n.(*ast.GoStmt)
		if !ok {
			return true
		}
		if isBlockedCall(gs.Call) {
			return true
		}
		label := goStmtLabel(gs)
		targets = append(targets, spec.Target{Label: label, Flag: literalGo})
		return true
	})
	if len(targets) < 2 {
		return nil
	}
	return &spec.Fanout{Gate: "goroutines", Targets: targets}
}

// commandWord is the bare lowercase command identifier shared by the
// stdlib dispatcher detection subject, the commands-directory name check,
// and the command-suffix stripper.
const commandWord = "command"

// isHandlerMethod reports whether name is an HTTP handler registration
// method name recognized by handlerFanout.
func isHandlerMethod(name string) bool {
	switch name {
	case "Handle", "HandleFunc",
		"Get", "Post", "Put", "Delete", "Patch",
		"Method", "Route", "Mount":
		return true
	}
	return false
}

// handlerFanout detects HTTP handler registration patterns in a function body.
// Returns a Fanout if 2+ handler registrations are found, else nil.
func handlerFanout(_ *token.FileSet, body *ast.BlockStmt) *spec.Fanout {
	var targets []spec.Target
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !isHandlerMethod(sel.Sel.Name) {
			return true
		}
		// Need at least 2 args: pattern, handler.
		if len(call.Args) < 2 {
			return true
		}
		// Skip handler targets that resolve to blocked stdlib calls.
		if handlerArgIsBlocked(call.Args[1]) {
			return true
		}
		flag := handlerFlag(call)
		label := handlerLabel(call.Args[1])
		targets = append(targets, spec.Target{Label: label, Flag: flag})
		return true
	})
	if len(targets) < 2 {
		return nil
	}
	return &spec.Fanout{Gate: gateHTTPRoutes, Targets: targets}
}

// handlerArgIsBlocked reports whether the handler argument expression is a
// selector resolving to a blocked stdlib call (e.g. http.NotFound).
func handlerArgIsBlocked(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && isBlockedStdlibCall(pkg.Name, sel.Sel.Name)
}

// routerClosureFanout detects HTTP route dispatch inside a returned
// http.HandlerFunc closure. Pattern:
//
//	func makeHandler(...) http.HandlerFunc {
//	  return func(w http.ResponseWriter, r *http.Request) {
//	    switch len(parts) {
//	    case 1: serveCollection(...)
//	    case 2: serveSpec(...)
//	    default: http.NotFound(...)
//	    }
//	  }
//	}
//
// Returns a Fanout with gate "HTTP routes" whose targets are the non-NotFound
// branch call labels, or nil if no qualifying closure is found.
func routerClosureFanout(fset *token.FileSet, body *ast.BlockStmt) *spec.Fanout {
	var targets []spec.Target
	ast.Inspect(body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return true
		}
		lit, ok := ret.Results[0].(*ast.FuncLit)
		if !ok {
			return true
		}
		// Walk the closure body for a switch or if/else-if chain; the first
		// qualifying construct stops the walk.
		ast.Inspect(lit.Body, func(inner ast.Node) bool {
			switch s := inner.(type) {
			case *ast.SwitchStmt:
				targets = append(targets, switchBranchTargets(fset, s)...)
				return false
			case *ast.IfStmt:
				targets = append(targets, ifChainTargets(fset, s)...)
				return false
			}
			return true
		})
		return false
	})
	if len(targets) < 2 {
		return nil
	}
	return &spec.Fanout{Gate: gateHTTPRoutes, Targets: targets}
}

// switchBranchTargets converts the case clauses of a routing switch into
// fanout targets, skipping ellipsis and NotFound labels.
func switchBranchTargets(fset *token.FileSet, s *ast.SwitchStmt) []spec.Target {
	var targets []spec.Target
	for _, stmt := range s.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		label := firstStmtLabel(fset, cc.Body)
		if label == "…" || label == labelNotFound {
			continue
		}
		flag := ""
		if len(cc.List) > 0 {
			flag = cleanCondition(exprString(fset, cc.List[0]))
		}
		targets = append(targets, spec.Target{Label: label, Flag: flag})
	}
	return targets
}

// ifChainTargets converts an if/else-if routing chain into fanout targets,
// skipping ellipsis and NotFound labels.
func ifChainTargets(fset *token.FileSet, s *ast.IfStmt) []spec.Target {
	var targets []spec.Target
	for cur := s; cur != nil; {
		label := firstStmtLabel(fset, cur.Body.List)
		if label != "…" && label != labelNotFound {
			flag := cleanCondition(exprString(fset, cur.Cond))
			targets = append(targets, spec.Target{Label: label, Flag: flag})
		}
		if next, ok := cur.Else.(*ast.IfStmt); ok {
			cur = next
		} else {
			cur = nil
		}
	}
	return targets
}

// handlerFlag extracts a flag from a handler registration call.
// Uses the first argument (route pattern) if it is a string literal,
// otherwise falls back to the method name.
func handlerFlag(call *ast.CallExpr) string {
	if len(call.Args) < 1 {
		return ""
	}
	if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
		s := lit.Value
		if len(s) >= 2 {
			s = s[1 : len(s)-1]
		}
		return s
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	return ""
}

// handlerLabel extracts a label from the handler argument (second arg)
// of a registration call.
func handlerLabel(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.CallExpr:
		// e.g. http.HandlerFunc(handler) — extract the inner name.
		if len(e.Args) > 0 {
			return handlerLabel(e.Args[0])
		}
		return argLabel(e)
	case *ast.FuncLit:
		return "anonymous"
	}
	return "handler"
}

// argLabel extracts a label from a call expression argument.
// "newInitCmd()" → "newInitCmd", "bar.New()" → "New".
func argLabel(expr ast.Expr) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		switch e := expr.(type) {
		case *ast.Ident:
			return e.Name
		case *ast.SelectorExpr:
			return e.Sel.Name
		}
		return "arg"
	}
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return "cmd"
}

// goStmtLabel extracts a label from a go statement.
// "go f()" → "f", "go func(){...}()" → "anonymous".
func goStmtLabel(gs *ast.GoStmt) string {
	switch fn := gs.Call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.FuncLit:
		return "anonymous"
	}
	return "goroutine"
}

// deriveFlag strips new/New prefix and Cmd/Command/cmd/command suffix, then
// lowercases. "newInitCmd" → "init", "newRenderCmd" → "render",
// "browserCmd" → "browser".
func deriveFlag(name string) string {
	s := stripPrefix(name, "new", "New")
	s = stripSuffix(s, "Command", "Cmd", commandWord, "cmd")
	return strings.ToLower(s)
}

// stripPrefix removes the first matching prefix that is shorter than s.
func stripPrefix(s string, prefixes ...string) string {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) && len(s) > len(prefix) {
			return s[len(prefix):]
		}
	}
	return s
}

// stripSuffix removes the first matching suffix that is shorter than s.
func stripSuffix(s string, suffixes ...string) string {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) && len(s) > len(suffix) {
			return s[:len(s)-len(suffix)]
		}
	}
	return s
}
