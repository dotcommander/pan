package scan

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// caseBranches converts the case clauses of a switch or type-switch body into
// fork branches. It is shared by switchFork and typeSwitchFork, whose only
// differences are the gate derivation and the statement node type.
func caseBranches(fset *token.FileSet, body []ast.Stmt) []spec.Branch {
	var branches []spec.Branch
	for _, stmt := range body {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		cond := conditionDefault
		if len(cc.List) > 0 {
			parts := make([]string, 0, len(cc.List))
			for _, expr := range cc.List {
				parts = append(parts, cleanCondition(exprString(fset, expr)))
			}
			cond = strings.Join(parts, ", ")
		}
		label := firstStmtLabel(fset, cc.Body)
		branches = append(branches, spec.Branch{Condition: cond, Label: label})
	}
	return branches
}

// finalizeBranches deduplicates branches and requires at least two survivors,
// mirroring the shared post-conditions of every fork constructor.
func finalizeBranches(branches []spec.Branch) []spec.Branch {
	if len(branches) < 2 {
		return nil
	}
	branches = dedupBranches(branches)
	if len(branches) < 2 {
		return nil
	}
	return branches
}

// switchFork converts a switch statement with 2+ case clauses into a Fork.
func switchFork(fset *token.FileSet, s *ast.SwitchStmt) *spec.Fork {
	branches := finalizeBranches(caseBranches(fset, s.Body.List))
	if branches == nil {
		return nil
	}
	gate := "switch"
	if s.Tag != nil {
		gate = cleanCondition(exprString(fset, s.Tag))
	}
	return &spec.Fork{Gate: gate, Branches: branches}
}

// typeSwitchFork converts a type-switch statement with 2+ case clauses into a Fork.
func typeSwitchFork(fset *token.FileSet, s *ast.TypeSwitchStmt) *spec.Fork {
	branches := finalizeBranches(caseBranches(fset, s.Body.List))
	if branches == nil {
		return nil
	}
	gate := "type"
	if s.Assign != nil {
		gate = cleanCondition(exprString(fset, s.Assign))
	}
	return &spec.Fork{Gate: gate, Branches: branches}
}

// selectFork converts a select statement with 2+ comm clauses into a Fork.
func selectFork(fset *token.FileSet, s *ast.SelectStmt) *spec.Fork {
	var branches []spec.Branch
	for _, stmt := range s.Body.List {
		cc, ok := stmt.(*ast.CommClause)
		if !ok {
			continue
		}
		cond := conditionDefault
		if cc.Comm != nil {
			cond = exprString(fset, cc.Comm)
		}
		label := firstStmtLabel(fset, cc.Body)
		branches = append(branches, spec.Branch{Condition: cond, Label: label})
	}
	if len(branches) < 2 {
		return nil
	}
	return &spec.Fork{Gate: "select", Branches: branches}
}

// ifFork converts an if/else-if chain with 2+ branches into a Fork.
func ifFork(fset *token.FileSet, s *ast.IfStmt) *spec.Fork {
	var branches []spec.Branch

	cur := s
	for cur != nil {
		cond := cleanCondition(exprString(fset, cur.Cond))
		label := firstStmtLabel(fset, cur.Body.List)
		branches = append(branches, spec.Branch{Condition: cond, Label: label})

		switch e := cur.Else.(type) {
		case *ast.IfStmt:
			cur = e
		case *ast.BlockStmt:
			label = firstStmtLabel(fset, e.List)
			branches = append(branches, spec.Branch{Condition: "else", Label: label})
			cur = nil
		default:
			cur = nil
		}
	}

	branches = finalizeBranches(branches)
	if branches == nil {
		return nil
	}
	return &spec.Fork{Gate: cleanCondition(exprString(fset, s.Cond)), Branches: branches}
}

// isIfElseFork reports whether a fork was generated from a simple if-else
// (not a switch/select). Heuristic: one branch has condition "else" or "…",
// which only ifFork produces.
func isIfElseFork(fork *spec.Fork) bool {
	for _, b := range fork.Branches {
		if b.Condition == "else" || b.Condition == "…" || strings.Contains(b.Condition, "/ else") {
			return true
		}
	}
	return false
}

// isHomogeneousFork reports whether <50% of a fork's branch labels are
// distinct. Homogeneous forks (e.g., all branches call "WriteByte") convey
// no meaningful routing information.
func isHomogeneousFork(fork *spec.Fork) bool {
	if len(fork.Branches) == 0 {
		return false
	}
	distinct := make(map[string]bool, len(fork.Branches))
	for _, b := range fork.Branches {
		distinct[b.Label] = true
	}
	diversity := float64(len(distinct)) / float64(len(fork.Branches))
	return diversity < 0.5
}

func meaningfulForkBranchCount(fork *spec.Fork) int {
	count := 0
	for _, branch := range fork.Branches {
		label := strings.TrimSpace(branch.Label)
		if !token.IsIdentifier(label) || types.Universe.Lookup(label) != nil {
			continue
		}
		count++
	}
	return count
}

// firstStmtLabel tries to derive a short label from the first statement in a
// block body. Falls back to "…" if no meaningful name is found.
// Calls that resolve to a blocked stdlib identifier (e.g. http.NotFound,
// log.Warn) are skipped so they never pollute fork branches or fanout targets.
func firstStmtLabel(fset *token.FileSet, stmts []ast.Stmt) string {
	for _, s := range stmts {
		if label, ok := stmtLabel(fset, s); ok {
			return label
		}
	}
	return "…"
}

// stmtLabel derives a label from one statement, reporting whether it is
// usable. Statements that resolve to blocked stdlib calls report !ok so the
// caller can consider the following statement instead.
func stmtLabel(fset *token.FileSet, s ast.Stmt) (string, bool) {
	switch st := s.(type) {
	case *ast.ExprStmt:
		if call, ok := st.X.(*ast.CallExpr); ok {
			if isBlockedCall(call) {
				return "", false
			}
			return callLabel(call), true
		}
	case *ast.ReturnStmt:
		return returnStmtLabel(fset, st)
	case *ast.AssignStmt:
		if label, ok := assignStmtLabel(st); ok {
			return label, true
		}
	}
	return "", false
}

// returnStmtLabel derives a label from a return statement: the first result
// expression, or the bare "return" for a valueless return.
func returnStmtLabel(fset *token.FileSet, st *ast.ReturnStmt) (string, bool) {
	if len(st.Results) == 0 {
		return "return", true
	}
	if call, ok := st.Results[0].(*ast.CallExpr); ok {
		if isBlockedCall(call) {
			return "", false
		}
		return callLabel(call), true
	}
	return "return " + exprString(fset, st.Results[0]), true
}

// assignStmtLabel derives a label from an assignment's right-hand side when
// it is a call expression that is not a blocked stdlib call.
func assignStmtLabel(st *ast.AssignStmt) (string, bool) {
	if len(st.Rhs) == 0 {
		return "", false
	}
	call, ok := st.Rhs[0].(*ast.CallExpr)
	if !ok || isBlockedCall(call) {
		return "", false
	}
	return callLabel(call), true
}

// isBlockedCall reports whether a call expression targets a blocked stdlib
// identifier (e.g. http.NotFound, log.Warn, fmt.Println). Uses
// isBlockedStdlibCall which is the single source of truth in mapper_filters.go.
func isBlockedCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return isBlockedStdlibCall(pkg.Name, sel.Sel.Name)
}

// callLabel extracts a readable label from a call expression.
func callLabel(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return "call"
}
