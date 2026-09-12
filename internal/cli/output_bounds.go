package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// boundedOutput describes a rendered text result after optional output caps.
type boundedOutput struct {
	Text      string
	Truncated bool
}

// formatBoundedText applies line and byte caps without splitting a UTF-8 rune.
func formatBoundedText(raw string, maxLines, maxBytes int) boundedOutput {
	if raw == "" {
		return boundedOutput{}
	}
	totalBytes := len(raw)
	lines := strings.SplitAfter(raw, "\n")
	totalLines := len(lines)
	if strings.HasSuffix(raw, "\n") {
		totalLines--
		lines = lines[:len(lines)-1]
	}

	var out strings.Builder
	shownLines, shownBytes := 0, 0
	truncated := false
	for _, line := range lines {
		if maxLines > 0 && shownLines >= maxLines {
			truncated = true
			break
		}
		shown, exhausted := boundedLine(line, maxBytes, shownBytes)
		if exhausted {
			out.WriteString(shown)
			shownBytes += len(shown)
			if len(shown) > 0 {
				shownLines++
			}
			truncated = true
			break
		}
		out.WriteString(shown)
		shownLines++
		shownBytes += len(shown)
	}
	if truncated {
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
			out.WriteByte('\n')
		}
		fmt.Fprintf(&out, "\n[Output truncated: showing %d of %d lines (%d of %d bytes). Narrow your query.]\n", shownLines, totalLines, shownBytes, totalBytes)
	}
	return boundedOutput{Text: out.String(), Truncated: truncated}
}

// boundedLine returns the complete line when it fits, otherwise its valid
// UTF-8 prefix and true. A zero byte cap is intentionally unlimited.
func boundedLine(line string, maxBytes, shownBytes int) (string, bool) {
	if maxBytes == 0 || shownBytes+len(line) <= maxBytes {
		return line, false
	}
	remaining := maxBytes - shownBytes
	if remaining <= 0 {
		return "", true
	}
	cut := remaining
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut], true
}
