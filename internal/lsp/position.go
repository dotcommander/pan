package lsp

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf16"
)

// SourcePosition finds identifier on a 1-based source line and returns the
// equivalent 0-based LSP position. LSP columns count UTF-16 code units.
func SourcePosition(file string, line int, identifier string) (sourceLine, utf16Column int, err error) {
	if line < 1 {
		return 0, 0, errors.New("line must be 1 or greater")
	}
	if strings.TrimSpace(identifier) == "" {
		return 0, 0, errors.New("identifier must not be blank")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return 0, 0, fmt.Errorf("read source file: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	if line > len(lines) {
		return 0, 0, fmt.Errorf("line %d is outside %s", line, file)
	}
	column := strings.Index(lines[line-1], identifier)
	if column < 0 {
		return 0, 0, fmt.Errorf("identifier %q was not found on line %d", identifier, line)
	}
	return line - 1, len(utf16.Encode([]rune(lines[line-1][:column]))), nil
}
