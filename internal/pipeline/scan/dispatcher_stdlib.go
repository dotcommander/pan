package scan

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// stdlibCommandFunction records the source location and direct local calls of
// a root package function. Package imports cannot express these edges.
type stdlibCommandFunction struct {
	file string
	decl *ast.FuncDecl
}

// findStdlibFlagCommandFiles detects a root package-main CLI which imports the
// standard flag package, declares string command constants, and dispatches
// three or more of those constants through switch cases. Dispatch subjects are
// a .command field (or a local command value in main), which keeps help and
// metadata switches from becoming runtime lanes. The returned maps use command
// values (rather than constant identifiers) as command names.
//
// Only constants present in switch cases are commands. This intentionally
// excludes canonicalization aliases such as commandConfigAlias = "config",
// which compare to a command but never select a command lane themselves.
func findStdlibFlagCommandFiles(root string) (map[string]string, map[string]map[string]bool) {
	commandFiles, commandLocalFiles, _ := analyzeStdlibFlagCommands(root)
	return commandFiles, commandLocalFiles
}

// CommandsReachingFunction returns the command lanes whose primary local call
// closure reaches target. The boolean reports whether target is a function in
// a recognized root-package stdlib dispatcher.
func CommandsReachingFunction(root, target string) ([]string, bool) {
	_, _, commandFunctions := analyzeStdlibFlagCommands(root)
	found := false
	var commands []string
	for command, functions := range commandFunctions {
		if functions[target] {
			found = true
			commands = append(commands, command)
		}
	}
	if !found {
		return nil, false
	}
	slices.Sort(commands)
	return commands, true
}

// stdlibCommandIndex captures one root package-main scan: the parsed files,
// the string constants they declare, and their top-level functions.
type stdlibCommandIndex struct {
	fset      *token.FileSet
	files     []*ast.File
	filePaths map[*ast.File]string
	functions map[string]stdlibCommandFunction
	constants map[string]string
	hasMain   bool
	hasFlag   bool
}

// analyzeStdlibFlagCommands recognizes the root stdlib-flag dispatcher shape
// and returns (dispatch entry files, same-package file closures, function
// closures) keyed by command value. Every map is nil when the shape is absent.
func analyzeStdlibFlagCommands(root string) (commandFiles map[string]string, localFiles map[string]map[string]bool, functionClosures map[string]map[string]bool) {
	index, ok := indexRootPackage(root)
	if !ok {
		return nil, nil, nil
	}

	commandConstants := index.switchCaseCommandConstants()
	if len(commandConstants) < 3 {
		return nil, nil, nil
	}

	collector := index.collectCommandBranches(commandConstants)
	if len(collector.commandFiles) < 3 {
		return nil, nil, nil
	}

	localFiles, functionClosures = resolveCommandClosures(collector.commandFiles, collector.commandFunctions, index.functions, commandConstants)
	return collector.commandFiles, localFiles, functionClosures
}

// indexRootPackage parses the root directory's non-test package-main Go files
// and indexes their string constants and top-level functions. The boolean is
// false when the directory is unreadable or lacks either a main function or a
// flag import.
func indexRootPackage(root string) (*stdlibCommandIndex, bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, false
	}
	index := &stdlibCommandIndex{
		fset:      token.NewFileSet(),
		filePaths: make(map[*ast.File]string, len(entries)),
		functions: make(map[string]stdlibCommandFunction),
		constants: make(map[string]string),
	}
	for _, entry := range entries {
		index.addEntry(root, entry)
	}
	if !index.hasMain || !index.hasFlag {
		return nil, false
	}
	return index, true
}

// addEntry folds one directory entry into the index, skipping directories,
// non-Go files, tests, unparseable files, and non-main packages.
func (idx *stdlibCommandIndex) addEntry(root string, entry os.DirEntry) {
	if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
		return
	}
	path := filepath.Join(root, entry.Name())
	file, parseErr := parser.ParseFile(idx.fset, path, nil, 0)
	if parseErr != nil || file.Name.Name != mainIdent {
		return
	}
	idx.files = append(idx.files, file)
	relPath := filepath.ToSlash(entry.Name())
	idx.filePaths[file] = relPath
	idx.hasFlag = idx.hasFlag || fileImportsFlag(file)
	collectStringConstants(file, idx.constants)
	idx.addFunctions(file, relPath)
}

// fileImportsFlag reports whether file imports the standard flag package.
func fileImportsFlag(file *ast.File) bool {
	for _, imp := range file.Imports {
		if importPath, err := strconv.Unquote(imp.Path.Value); err == nil && importPath == "flag" {
			return true
		}
	}
	return false
}

// addFunctions indexes file's top-level function declarations, tracking the
// presence of a main function.
func (idx *stdlibCommandIndex) addFunctions(file *ast.File, relPath string) {
	for _, decl := range file.Decls {
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok || funcDecl.Recv != nil || funcDecl.Body == nil {
			continue
		}
		if funcDecl.Name.Name == mainIdent {
			idx.hasMain = true
		}
		idx.functions[funcDecl.Name.Name] = stdlibCommandFunction{file: relPath, decl: funcDecl}
	}
}

// switchCaseCommandConstants returns the string constants selected by switch
// cases across the indexed files. This is the canonical command set;
// comparison-only constants never select a command lane themselves.
func (idx *stdlibCommandIndex) switchCaseCommandConstants() map[string]string {
	commandConstants := make(map[string]string)
	for _, file := range idx.files {
		for _, ident := range switchCaseIdents(file) {
			if value, exists := idx.constants[ident]; exists {
				commandConstants[ident] = value
			}
		}
	}
	return commandConstants
}

// switchCaseIdents collects the identifier expressions used as switch-case
// values anywhere in file.
func switchCaseIdents(file *ast.File) []string {
	var idents []string
	ast.Inspect(file, func(node ast.Node) bool {
		switchStmt, ok := node.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		for _, stmt := range switchStmt.Body.List {
			clause, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, expr := range clause.List {
				if ident, ok := expr.(*ast.Ident); ok {
					idents = append(idents, ident.Name)
				}
			}
		}
		return true
	})
	return idents
}

// commandBranchCollector accumulates dispatch evidence per command: the entry
// file (best-ranked), and the functions each command's branches call.
type commandBranchCollector struct {
	currentFile      string
	commandFiles     map[string]string
	commandFileRanks map[string]int
	commandFunctions map[string]map[string]bool
}

// collectCommandBranches walks every indexed top-level function and records
// the command-selecting switch cases and comparisons it contains.
func (idx *stdlibCommandIndex) collectCommandBranches(commandConstants map[string]string) *commandBranchCollector {
	collector := &commandBranchCollector{
		commandFiles:     make(map[string]string, len(commandConstants)),
		commandFileRanks: make(map[string]int, len(commandConstants)),
		commandFunctions: make(map[string]map[string]bool, len(commandConstants)),
	}
	for _, file := range idx.files {
		for _, decl := range file.Decls {
			funcDecl, ok := decl.(*ast.FuncDecl)
			if !ok || funcDecl.Recv != nil || funcDecl.Body == nil {
				continue
			}
			collector.inspectFunction(funcDecl, idx.filePaths[file], commandConstants)
		}
	}
	return collector
}

// inspectFunction records the command branches selected inside one function.
func (c *commandBranchCollector) inspectFunction(function *ast.FuncDecl, file string, commandConstants map[string]string) {
	c.currentFile = file
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch stmt := node.(type) {
		case *ast.SwitchStmt:
			c.inspectSwitch(function.Name.Name, stmt, commandConstants)
		case *ast.IfStmt:
			c.inspectIf(function.Name.Name, stmt, commandConstants)
		}
		return true
	})
}

// inspectSwitch records the command constants selected by one dispatch switch.
func (c *commandBranchCollector) inspectSwitch(function string, stmt *ast.SwitchStmt, commandConstants map[string]string) {
	rank, dispatches := commandDispatchRank(function, stmt.Tag)
	if !dispatches {
		return
	}
	for _, bodyStmt := range stmt.Body.List {
		clause, ok := bodyStmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		calls := directCallsInStatements(clause.Body)
		for _, expr := range clause.List {
			ident, ok := expr.(*ast.Ident)
			if !ok {
				continue
			}
			if command, exists := commandConstants[ident.Name]; exists {
				c.add(command, rank, calls)
			}
		}
	}
}

// inspectIf records the command constants compared by one dispatch if.
func (c *commandBranchCollector) inspectIf(function string, stmt *ast.IfStmt, commandConstants map[string]string) {
	rank, dispatches := commandComparisonRank(function, stmt.Cond)
	if !dispatches {
		return
	}
	calls := directCallsInStatements(stmt.Body.List)
	for _, command := range commandsInComparison(stmt.Cond, commandConstants) {
		c.add(command, rank, calls)
	}
}

// add records one command branch: its dispatch entry file when it outranks the
// current one, and every direct call its body makes.
func (c *commandBranchCollector) add(command string, rank int, calls []string) {
	if len(calls) == 0 {
		return
	}
	if oldRank, exists := c.commandFileRanks[command]; !exists || rank < oldRank {
		c.commandFiles[command] = c.currentFile
		c.commandFileRanks[command] = rank
	}
	if c.commandFunctions[command] == nil {
		c.commandFunctions[command] = make(map[string]bool)
	}
	for _, call := range calls {
		c.commandFunctions[command][call] = true
	}
}

// collectStringConstants records every string constant literal declared in
// file, keyed by identifier name.
func collectStringConstants(file *ast.File, constants map[string]string) {
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}
		for _, spec := range genDecl.Specs {
			collectStringConstantSpec(spec, constants)
		}
	}
}

// collectStringConstantSpec records the string literals of one const spec.
func collectStringConstantSpec(spec ast.Spec, constants map[string]string) {
	valueSpec, ok := spec.(*ast.ValueSpec)
	if !ok || len(valueSpec.Names) != len(valueSpec.Values) {
		return
	}
	for index, name := range valueSpec.Names {
		literal, ok := valueSpec.Values[index].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			continue
		}
		if value, err := strconv.Unquote(literal.Value); err == nil {
			constants[name.Name] = value
		}
	}
}

// resolveCommandClosures walks the local call graph from each command's
// dispatch functions (seeded with main) and returns the same-package files and
// functions each command reaches.
func resolveCommandClosures(commandFiles map[string]string, commandFunctions map[string]map[string]bool, functions map[string]stdlibCommandFunction, commandConstants map[string]string) (localFileClosures map[string]map[string]bool, functionClosures map[string]map[string]bool) {
	localFileClosures = make(map[string]map[string]bool, len(commandFiles))
	functionClosures = make(map[string]map[string]bool, len(commandFiles))
	for command, entryFile := range commandFiles {
		localFiles := map[string]bool{entryFile: true}
		queue := []string{mainIdent}
		for function := range commandFunctions[command] {
			queue = append(queue, function)
		}
		seen := make(map[string]bool)
		reached := make(map[string]bool)
		for len(queue) > 0 {
			function := queue[0]
			queue = queue[1:]
			if seen[function] {
				continue
			}
			seen[function] = true
			info, exists := functions[function]
			if !exists {
				continue
			}
			reached[function] = true
			localFiles[info.file] = true
			queue = append(queue, directLocalCallsForCommand(info.decl, command, commandConstants)...)
		}
		localFileClosures[command] = localFiles
		functionClosures[command] = reached
	}
	return localFileClosures, functionClosures
}

// commandDispatchRank recognizes runtime command selection. The root main
// function is preferred when it owns a plain local command value; elsewhere a
// selector named command is the stable Prompter-style signal. A lower rank wins
// as the lane's displayed dispatch file while every recognized branch still
// contributes to its same-package closure.
func commandDispatchRank(function string, subject ast.Expr) (int, bool) {
	if isCommandSelector(subject) {
		if function == mainIdent {
			return 0, true
		}
		return 1, true
	}
	ident, ok := subject.(*ast.Ident)
	return 0, ok && function == mainIdent && ident.Name == commandWord
}

func commandComparisonRank(function string, condition ast.Expr) (int, bool) {
	hasCommandSelector := false
	hasMainCommand := false
	ast.Inspect(condition, func(node ast.Node) bool {
		switch expr := node.(type) {
		case *ast.SelectorExpr:
			if expr.Sel.Name == commandWord {
				hasCommandSelector = true
			}
		case *ast.Ident:
			if function == mainIdent && expr.Name == commandWord {
				hasMainCommand = true
			}
		}
		return true
	})
	if hasCommandSelector {
		if function == mainIdent {
			return 0, true
		}
		return 1, true
	}
	return 0, hasMainCommand
}

func isCommandSelector(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == commandWord
}

func commandsInComparison(expr ast.Expr, commandConstants map[string]string) []string {
	commands := make(map[string]bool)
	ast.Inspect(expr, func(node ast.Node) bool {
		binary, ok := node.(*ast.BinaryExpr)
		if !ok || binary.Op != token.EQL {
			return true
		}
		for _, side := range []ast.Expr{binary.X, binary.Y} {
			ident, ok := side.(*ast.Ident)
			if !ok {
				continue
			}
			if command, exists := commandConstants[ident.Name]; exists {
				commands[command] = true
			}
		}
		return true
	})
	result := make([]string, 0, len(commands))
	for command := range commands {
		result = append(result, command)
	}
	return result
}

// commandCallWalker follows one command through a function body.
// Command-selecting switches and ifs contribute only the selected branch, and
// a selected return stops traversal of statements that are unreachable for
// that command. This keeps a shared main function from assigning its post-
// dispatch pipeline to commands that return from an earlier branch.
type commandCallWalker struct {
	function         string
	command          string
	commandConstants map[string]string
	shadowed         map[string]bool
	calls            map[string]bool
}

// directLocalCallsForCommand returns the sorted set of package-local functions
// the given command reaches inside function.
func directLocalCallsForCommand(function *ast.FuncDecl, command string, commandConstants map[string]string) []string {
	walker := &commandCallWalker{
		function:         function.Name.Name,
		command:          command,
		commandConstants: commandConstants,
		shadowed:         make(map[string]bool),
		calls:            make(map[string]bool),
	}
	collectFieldNames(function.Type.Params, walker.shadowed)
	collectFieldNames(function.Type.Results, walker.shadowed)
	walker.walkStatements(function.Body.List)
	result := make([]string, 0, len(walker.calls))
	for call := range walker.calls {
		result = append(result, call)
	}
	slices.Sort(result)
	return result
}

// addCalls records the direct local calls of statements, excluding calls to
// names shadowed by the enclosing function's parameters and results.
func (w *commandCallWalker) addCalls(statements []ast.Stmt) {
	for _, call := range directCallsInStatementsExcluding(statements, w.shadowed) {
		w.calls[call] = true
	}
}

// walkStatements processes one statement list and reports whether every path
// through it returns.
func (w *commandCallWalker) walkStatements(statements []ast.Stmt) bool {
	for _, statement := range statements {
		switch stmt := statement.(type) {
		case *ast.ReturnStmt:
			w.addCalls([]ast.Stmt{stmt})
			return true
		case *ast.BlockStmt:
			if w.walkStatements(stmt.List) {
				return true
			}
		case *ast.IfStmt:
			if w.walkIf(stmt) {
				return true
			}
		case *ast.SwitchStmt:
			if w.walkSwitch(stmt) {
				return true
			}
		default:
			w.addCalls([]ast.Stmt{statement})
		}
	}
	return false
}

// walkIf processes one if statement for the tracked command and reports
// whether every path through it returns.
func (w *commandCallWalker) walkIf(stmt *ast.IfStmt) bool {
	if stmt.Init != nil {
		w.addCalls([]ast.Stmt{stmt.Init})
	}
	if _, dispatches := commandComparisonRank(w.function, stmt.Cond); dispatches {
		return w.walkDispatchIf(stmt)
	}
	w.addCallsFromExpr(stmt.Cond)
	if statementsAlwaysReturn(stmt.Body.List) && stmt.Else == nil {
		// Error/help guards are terminal side paths, not part of the
		// command's primary fall-through execution spine.
		return false
	}
	bodyReturns := w.walkStatements(stmt.Body.List)
	elseReturns := w.walkElse(stmt.Else)
	return stmt.Else != nil && bodyReturns && elseReturns
}

// walkDispatchIf follows only the branch of a command-comparison if that
// selects the tracked command.
func (w *commandCallWalker) walkDispatchIf(stmt *ast.IfStmt) bool {
	selected := slices.Contains(commandsInComparison(stmt.Cond, w.commandConstants), w.command)
	if selected {
		return w.walkStatements(stmt.Body.List)
	}
	return w.walkElse(stmt.Else)
}

// walkSwitch processes one switch statement for the tracked command and
// reports whether every path through it returns.
func (w *commandCallWalker) walkSwitch(stmt *ast.SwitchStmt) bool {
	if stmt.Init != nil {
		w.addCalls([]ast.Stmt{stmt.Init})
	}
	if _, dispatches := commandDispatchRank(w.function, stmt.Tag); dispatches {
		return w.walkSelectedClause(stmt)
	}
	if stmt.Tag == nil {
		if clause := selectedCommandClause(stmt, w.command, w.commandConstants); clause != nil {
			return w.walkStatements(clause.Body)
		}
	}
	for _, rawClause := range stmt.Body.List {
		if clause, ok := rawClause.(*ast.CaseClause); ok {
			w.walkStatements(clause.Body)
		}
	}
	return false
}

// walkSelectedClause follows only the case clause a dispatch switch selects
// for the tracked command.
func (w *commandCallWalker) walkSelectedClause(stmt *ast.SwitchStmt) bool {
	if clause := selectedCommandClause(stmt, w.command, w.commandConstants); clause != nil {
		return w.walkStatements(clause.Body)
	}
	return false
}

// walkElse processes an else branch and reports whether every path through it
// returns.
func (w *commandCallWalker) walkElse(statement ast.Stmt) bool {
	switch branch := statement.(type) {
	case nil:
		return false
	case *ast.BlockStmt:
		return w.walkStatements(branch.List)
	case *ast.IfStmt:
		return w.walkStatements([]ast.Stmt{branch})
	default:
		w.addCalls([]ast.Stmt{statement})
		return false
	}
}

// addCallsFromExpr records direct local calls inside an expression, without
// descending into nested function literals.
func (w *commandCallWalker) addCallsFromExpr(expr ast.Expr) {
	ast.Inspect(expr, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && !w.shadowed[ident.Name] {
			w.calls[ident.Name] = true
		}
		return true
	})
}

func statementsAlwaysReturn(statements []ast.Stmt) bool {
	if len(statements) == 0 {
		return false
	}
	return statementAlwaysReturns(statements[len(statements)-1])
}

func statementAlwaysReturns(statement ast.Stmt) bool {
	switch stmt := statement.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BlockStmt:
		return statementsAlwaysReturn(stmt.List)
	case *ast.IfStmt:
		return stmt.Else != nil && statementsAlwaysReturn(stmt.Body.List) && statementAlwaysReturns(stmt.Else)
	case *ast.SwitchStmt:
		return switchAlwaysReturns(stmt)
	default:
		return false
	}
}

// switchAlwaysReturns reports whether every clause of a switch returns and it
// has a default clause.
func switchAlwaysReturns(stmt *ast.SwitchStmt) bool {
	hasDefault := false
	if len(stmt.Body.List) == 0 {
		return false
	}
	for _, rawClause := range stmt.Body.List {
		clause, ok := rawClause.(*ast.CaseClause)
		if !ok || !statementsAlwaysReturn(clause.Body) {
			return false
		}
		if len(clause.List) == 0 {
			hasDefault = true
		}
	}
	return hasDefault
}

// clauseCommandMatch classifies one case-clause expression against the
// tracked command.
type clauseCommandMatch struct {
	mentionsCommand bool // the expression references any known command
}

// selectedCommandClause returns the case clause a switch selects for command:
// its direct command case when present, else the default clause when any
// command case exists, else nil.
func selectedCommandClause(stmt *ast.SwitchStmt, command string, commandConstants map[string]string) *ast.CaseClause {
	var defaultClause *ast.CaseClause
	hasCommandCase := false
	for _, rawClause := range stmt.Body.List {
		clause, ok := rawClause.(*ast.CaseClause)
		if !ok {
			continue
		}
		if len(clause.List) == 0 {
			defaultClause = clause
			continue
		}
		match, resolved := matchClauseExpressions(clause, command, commandConstants)
		if resolved {
			return clause
		}
		hasCommandCase = hasCommandCase || match.mentionsCommand
	}
	if hasCommandCase {
		return defaultClause
	}
	return nil
}

// matchClauseExpressions classifies every expression in one non-default
// clause. The boolean is true when the clause directly selects the tracked
// command.
func matchClauseExpressions(clause *ast.CaseClause, command string, commandConstants map[string]string) (clauseCommandMatch, bool) {
	match := clauseCommandMatch{}
	for _, expression := range clause.List {
		if ident, ok := expression.(*ast.Ident); ok {
			if candidate, exists := commandConstants[ident.Name]; exists {
				match.mentionsCommand = true
				if candidate == command {
					return match, true
				}
			}
			continue
		}
		commands := commandsInComparison(expression, commandConstants)
		match.mentionsCommand = match.mentionsCommand || len(commands) > 0
		if slices.Contains(commands, command) {
			return match, true
		}
	}
	return match, false
}

func directCallsInStatements(statements []ast.Stmt) []string {
	return directCallsInStatementsExcluding(statements, nil)
}

func directCallsInStatementsExcluding(statements []ast.Stmt, shadowed map[string]bool) []string {
	calls := make(map[string]bool)
	for _, statement := range statements {
		ast.Inspect(statement, func(node ast.Node) bool {
			if _, ok := node.(*ast.FuncLit); ok {
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if ok && !shadowed[ident.Name] {
				calls[ident.Name] = true
			}
			return true
		})
	}
	result := make([]string, 0, len(calls))
	for call := range calls {
		result = append(result, call)
	}
	return result
}

func collectFieldNames(fields *ast.FieldList, names map[string]bool) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			names[name.Name] = true
		}
	}
}
