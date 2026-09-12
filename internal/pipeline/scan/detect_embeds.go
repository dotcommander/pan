package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// detectEmbeds reads a file line-by-line and extracts //go:embed directives.
// Returns map[storeName]Writer and the list of resolved (relative-to-root)
// file paths matching the embed patterns.
func detectEmbeds(path, root string) (map[string]spec.Writer, []string) {
	result := make(map[string]spec.Writer)
	f, err := os.Open(path)
	if err != nil {
		return result, nil
	}
	defer func() { _ = f.Close() }()

	scan := embedScan{srcDir: filepath.Dir(path), root: root, writers: result}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "//go:embed ") {
			scan.directive(line, scanner)
		}
	}
	return scan.writers, scan.paths
}

// embedScan accumulates the embed stores and resolved file paths of one file.
type embedScan struct {
	srcDir  string
	root    string
	writers map[string]spec.Writer
	paths   []string
}

// directive processes one //go:embed line: it binds the following var
// declaration as the store writer, then resolves the pattern to files.
func (s *embedScan) directive(line string, scanner *bufio.Scanner) {
	embedPat := strings.TrimSpace(strings.TrimPrefix(line, "//go:embed "))
	s.bindVarName(scanner, embedPat)
	s.resolvePattern(embedPat)
}

// bindVarName records the embed.FS store for the next non-empty line's var
// declaration, which names the writing stage.
func (s *embedScan) bindVarName(scanner *bufio.Scanner, embedPat string) {
	for scanner.Scan() {
		next := strings.TrimSpace(scanner.Text())
		if next == "" {
			continue
		}
		storeName := "embed.FS " + embedPat
		s.writers[storeName] = spec.Writer{Stage: extractVarName(next), Access: "r"}
		return
	}
}

// resolvePattern resolves one embed pattern relative to the scanned file's
// directory and records the root-relative matches.
func (s *embedScan) resolvePattern(embedPat string) {
	matches, err := filepath.Glob(filepath.Join(s.srcDir, embedPat))
	if err != nil {
		return
	}
	for _, match := range matches {
		if rel, relErr := filepath.Rel(s.root, match); relErr == nil {
			s.paths = append(s.paths, rel)
		}
	}
}

// extractVarName pulls the identifier from a var declaration line.
// "var TemplateFS embed.FS" → "TemplateFS"
// "	TemplateFS embed.FS" → "TemplateFS"
func extractVarName(line string) string {
	line = strings.TrimPrefix(line, "var ")
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "embed"
	}
	return fields[0]
}
