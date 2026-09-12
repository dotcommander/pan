package scan

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// scanGoEffects uses Go's parser so effect evidence identifies the concrete
// operation and distinguishes AST-confirmed leads from heuristic fallback.
func scanGoEffects(lines []sourceLine, patterns []effectPattern) ([]Effect, error) {
	source := make([]string, 0, len(lines))
	for _, line := range lines {
		source = append(source, line.text)
	}
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "effects.go", strings.Join(source, "\n"), parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var effects []Effect
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CallExpr:
			op := goEffectOperation(value.Fun)
			for _, pattern := range patterns {
				if pattern.Re.MatchString(op + "(") {
					effects = append(effects, Effect{Kind: pattern.Kind, Op: op, Line: fset.Position(value.Pos()).Line, Lane: pattern.Lane, Evidence: evidenceParsedCall, Provenance: "go_ast_call"})
				}
			}
		case *ast.GoStmt:
			effects = append(effects, Effect{Kind: kindGoroutine, Op: "go", Line: fset.Position(value.Pos()).Line, Lane: laneLifecycleConcurrency, Evidence: "parsed go statement", Provenance: "go_ast_statement"})
		}
		return true
	})
	for _, effect := range scanEffectLines(lines, patterns) {
		if effect.Kind == kindSecret || effect.Kind == kindCrypto {
			effects = append(effects, effect)
		}
	}
	return normalizeEffects(effects), nil
}

func goEffectOperation(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		prefix := goEffectOperation(value.X)
		if prefix == "" {
			return value.Sel.Name
		}
		return prefix + "." + value.Sel.Name
	default:
		return ""
	}
}
