package improve

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// ValidateTestChanges applies the containment checks while requiring every
// generated change to be a non-empty Go test. Prep cannot alter production.
func ValidateTestChanges(root string, changes []FileChange) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	if len(changes) == 0 {
		return errors.New("empty test generation")
	}
	for _, change := range changes {
		if err := validateTestChange(rootAbs, change); err != nil {
			return err
		}
	}
	return nil
}

func validateTestChange(rootAbs string, change FileChange) error {
	if strings.TrimSpace(change.FilePath) == "" || filepath.IsAbs(change.FilePath) {
		return fmt.Errorf("invalid test file path %q", change.FilePath)
	}
	clean := filepath.Clean(filepath.FromSlash(change.FilePath))
	if clean == parentDir || strings.HasPrefix(clean, parentDir+string(filepath.Separator)) || !strings.HasSuffix(clean, "_test.go") {
		return fmt.Errorf("test generation path %q is not a contained _test.go file", change.FilePath)
	}
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		if part == gitDirName {
			return fmt.Errorf("test generation path %q enters .git", change.FilePath)
		}
	}
	if strings.TrimSpace(change.NewContents) == "" {
		return fmt.Errorf("test generation may not delete %q", change.FilePath)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), filepath.Join(rootAbs, clean), change.NewContents, parser.AllErrors); err != nil {
		return fmt.Errorf("parse generated test %q: %w", change.FilePath, err)
	}
	return nil
}
