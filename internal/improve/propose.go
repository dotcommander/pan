package improve

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const parentDir = ".."

// ProposeDeadCode builds the deterministic deletion proposal for root:
// every unexported top-level declaration with zero lexical references
// module-wide (tests included) is removed by AST surgery and reformatting.
// An empty Changes set means no dead code was found — the caller records
// the proposal outcome. Proposals are whole-file replacements; the
// proposer never emits deletions or test-file edits.
func ProposeDeadCode(root string, exclude []string) (*Proposal, error) {
	dead, err := DetectDeadSymbols(root, exclude)
	if err != nil {
		return nil, err
	}
	if len(dead) == 0 {
		files, listErr := listGoFiles(root, exclude)
		if listErr != nil {
			return nil, listErr
		}
		checked := make([]string, 0, min(len(files), 50))
		for _, file := range files {
			checked = append(checked, relSlash(root, file))
			if len(checked) == 50 {
				break
			}
		}
		limitations := []string{"lexical references only; semantic and runtime reachability not checked"}
		if len(files) > len(checked) {
			limitations = append(limitations, fmt.Sprintf("checked list truncated: %d of %d Go files shown", len(checked), len(files)))
		}
		return &Proposal{
			Rationale:   "no unexported declarations with zero references found; nothing to remove",
			Changes:     []FileChange{},
			Disposition: string(OutcomeNoCandidate),
			Checked:     checked,
			Limitations: limitations,
			Source:      ProposalSource,
		}, nil
	}

	byFile := make(map[string][]DeadSymbol)
	var files []string
	for _, symbol := range dead {
		if _, seen := byFile[symbol.File]; !seen {
			files = append(files, symbol.File)
		}
		byFile[symbol.File] = append(byFile[symbol.File], symbol)
	}
	sort.Strings(files)

	var changes []FileChange
	var removed []DeadSymbol
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		names := make(map[string]bool, len(byFile[rel]))
		for _, symbol := range byFile[rel] {
			names[symbol.Name] = true
		}
		out, dropped := removeDeclarations(src, names)
		if len(dropped) == 0 {
			continue
		}
		changes = append(changes, FileChange{
			FilePath:    rel,
			NewContents: string(out),
			Reasoning:   fmt.Sprintf("remove %d unexported declaration(s) with zero references: %s", len(dropped), strings.Join(dropped, ", ")),
		})
		for _, symbol := range byFile[rel] {
			if slicesContains(dropped, symbol.Name) {
				removed = append(removed, symbol)
			}
		}
	}

	return &Proposal{
		Rationale:  fmt.Sprintf("remove %d unexported declaration(s) with zero module-wide references (deterministic lexical evidence)", len(removed)),
		Changes:    changes,
		Candidates: removed,
		Source:     ProposalSource,
	}, nil
}

func slicesContains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// removeDeclarations deletes every top-level declaration whose declared
// names are all in the removal set, reformats the file, and returns the
// rewritten source plus the sorted names actually dropped. Value groups
// (var/const blocks) survive with only their dead specs removed.
func removeDeclarations(src []byte, names map[string]bool) ([]byte, []string) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "src.go", src, parser.ParseComments)
	if err != nil {
		// The file parsed during detection; a parse failure here is a
		// defect in this function's contract, so refuse rather than guess.
		return src, nil
	}

	var kept []ast.Decl
	var dropped []string
	for _, decl := range file.Decls {
		survivor, names2 := partitionDecl(decl, names)
		dropped = append(dropped, names2...)
		if survivor != nil {
			kept = append(kept, survivor)
		}
	}
	if len(dropped) == 0 {
		return src, nil
	}

	file.Decls = kept
	var out bytes.Buffer
	if err := format.Node(&out, fset, file); err != nil {
		return src, nil
	}
	sort.Strings(dropped)
	return out.Bytes(), dropped
}

// partitionDecl decides one declaration's fate under removal surgery: it
// returns the (possibly spec-pruned) declaration to keep — nil when the
// whole declaration is dropped — plus the declared names removed from it.
// Methods are addressed by name; the receiver survives because its type is
// only dead when the type itself is.
func partitionDecl(decl ast.Decl, names map[string]bool) (ast.Decl, []string) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if declToRemove(d, names) {
			return nil, []string{d.Name.Name}
		}
		return decl, nil
	case *ast.GenDecl:
		return partitionGenDecl(d, names)
	}
	return decl, nil
}

// declToRemove reports whether one function or method declaration is in
// the removal set; test functions are never removed.
func declToRemove(d *ast.FuncDecl, names map[string]bool) bool {
	name := d.Name.Name
	if d.Recv != nil {
		return names[name]
	}
	return names[name] && !strings.HasPrefix(name, testPrefix)
}

// partitionGenDecl prunes dead specs from one var/const/type declaration,
// dropping the whole declaration when every spec is dead. Other token
// kinds (imports, anything else) are kept verbatim.
func partitionGenDecl(d *ast.GenDecl, names map[string]bool) (ast.Decl, []string) {
	if d.Tok != token.VAR && d.Tok != token.CONST && d.Tok != token.TYPE {
		return d, nil
	}
	var keptSpecs []ast.Spec
	var dropped []string
	for _, spec := range d.Specs {
		specNames := declaredSpecNames(spec)
		if len(specNames) > 0 && allNamesIn(specNames, names) {
			dropped = append(dropped, specNames...)
			continue
		}
		keptSpecs = append(keptSpecs, spec)
	}
	if len(keptSpecs) == 0 {
		return nil, dropped // every spec was dead: drop the whole declaration
	}
	d.Specs = keptSpecs
	return d, dropped
}

// declaredSpecNames lists the names a gen decl spec declares.
func declaredSpecNames(spec ast.Spec) []string {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return []string{s.Name.Name}
	case *ast.ValueSpec:
		var names []string
		for _, id := range s.Names {
			names = append(names, id.Name)
		}
		return names
	}
	return nil
}

func allNamesIn(names []string, set map[string]bool) bool {
	for _, name := range names {
		if !set[name] {
			return false
		}
	}
	return true
}

// ValidateProposal enforces the pre-apply safety contract: paths must be
// slash-relative, stay inside the target root, avoid .git and test files,
// and never be empty. The deterministic proposer satisfies all of this by
// construction; validation exists so a future proposer cannot bypass the
// gate.
func ValidateProposal(root string, changes []FileChange) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	for _, change := range changes {
		if err := validateChange(rootAbs, change); err != nil {
			return err
		}
	}
	return nil
}

// validateChange enforces the pre-apply safety contract on one change:
// the path must be slash-relative, stay inside rootAbs, avoid .git and
// test files, and never be empty; a deletion must target an existing file.
func validateChange(rootAbs string, change FileChange) error {
	if strings.TrimSpace(change.FilePath) == "" {
		return errors.New("empty file path in proposal")
	}
	full, err := SafeProposalPath(rootAbs, change.FilePath)
	if err != nil {
		return err
	}
	clean, err := filepath.Rel(rootAbs, full)
	if err != nil {
		return fmt.Errorf("resolve proposal path %q: %w", change.FilePath, err)
	}
	parts := strings.Split(filepath.ToSlash(clean), "/")
	for _, part := range parts {
		if part == gitDirName {
			return fmt.Errorf("path %q enters .git", change.FilePath)
		}
	}
	if strings.HasSuffix(clean, "_test.go") {
		return fmt.Errorf("proposal modifies test file %q", change.FilePath)
	}
	if strings.TrimSpace(change.NewContents) == "" {
		if _, err := os.Stat(full); err != nil {
			return fmt.Errorf("delete target %q does not exist", change.FilePath)
		}
	}
	return nil
}

// SafeProposalPath resolves a proposal path inside root and rejects absolute,
// escaping, and symlink-traversing paths. ApplyProposal uses it too because
// prep proposals may intentionally write test files that ValidateProposal
// correctly forbids for refactors.
func SafeProposalPath(root, filePath string) (string, error) {
	if strings.TrimSpace(filePath) == "" {
		return "", errors.New("empty file path in proposal")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	if filepath.IsAbs(filePath) {
		return "", fmt.Errorf("absolute path %q not allowed", filePath)
	}
	full := filepath.Join(rootAbs, filepath.Clean(filepath.FromSlash(filePath)))
	rel, err := filepath.Rel(rootAbs, full)
	if err != nil || rel == "." || rel == parentDir || strings.HasPrefix(rel, parentDir+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes repo root", filePath)
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == gitDirName {
			return "", fmt.Errorf("path %q enters .git", filePath)
		}
	}
	if err := rejectProposalSymlinks(rootAbs, rel, filePath); err != nil {
		return "", err
	}
	return full, nil
}

func rejectProposalSymlinks(root, rel, original string) error {
	current := root
	for _, part := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect proposal path %q: %w", original, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path %q traverses symlink %q", original, current)
		}
	}
	return nil
}

// ApplyProposal writes whole-file replacements into dir. Empty contents
// delete the file (Pan improvement semantics; the deterministic proposer never
// emits deletions today). Parent directories are created as needed.
func ApplyProposal(dir string, changes []FileChange) error {
	for _, change := range changes {
		full, err := SafeProposalPath(dir, change.FilePath)
		if err != nil {
			return err
		}
		if strings.TrimSpace(change.NewContents) == "" {
			if removeErr := os.Remove(full); removeErr != nil && !os.IsNotExist(removeErr) {
				return fmt.Errorf("delete %s: %w", change.FilePath, removeErr)
			}
			continue
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(full), dstDirPerm); mkdirErr != nil {
			return fmt.Errorf("create parent of %s: %w", change.FilePath, mkdirErr)
		}
		info, err := os.Stat(full)
		mode := os.FileMode(0o644)
		if err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(full, []byte(change.NewContents), mode); err != nil {
			return fmt.Errorf("write %s: %w", change.FilePath, err)
		}
	}
	return nil
}
