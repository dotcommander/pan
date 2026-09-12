package lsp

import (
	"context"
	"path/filepath"
	"strings"
)

// ReferenceResult is a bounded semantic reference query. Result keeps the
// typed unavailable contract used by Query; TestOnly is true only when at
// least one reference was returned and every reference is from a Go test.
type ReferenceResult struct {
	Result   QueryResult
	TestOnly bool
}

// References resolves identifier references at a one-based source line. It
// starts and shuts down the configured local language server for this query,
// exactly as Service.Query does. A missing server or unsupported language is
// returned as a typed unavailable Result rather than a process error.
func (s *Service) References(ctx context.Context, root, file string, line int, identifier string) (ReferenceResult, error) {
	absFile, err := filepath.Abs(file)
	if err != nil {
		return ReferenceResult{}, err
	}
	zeroLine, column, err := SourcePosition(absFile, line, identifier)
	if err != nil {
		return ReferenceResult{}, err
	}
	result, err := s.Query(ctx, root, QueryRequest{Capability: CapabilityRefs, File: absFile, Line: zeroLine, Column: column})
	if err != nil {
		return ReferenceResult{}, err
	}
	refs := result.Locations
	if len(refs) == 0 {
		return ReferenceResult{Result: result}, nil
	}
	for _, ref := range refs {
		if !strings.HasSuffix(ref.Path, "_test.go") {
			return ReferenceResult{Result: result}, nil
		}
	}
	return ReferenceResult{Result: result, TestOnly: true}, nil
}
