package reportimport

import "strings"

// Raw evidence is already valid. Scan it without decoded string allocations,
// charging the same escaping/indentation as encoding/json's RawMessage path.
func (s *jsonSizer) raw(data []byte, depth int) error {
	inString := false
	for i := 0; i < len(data); i++ {
		if i%4096 == 0 {
			if err := s.ctx.Err(); err != nil {
				return err
			}
		}
		b := data[i]
		if inString {
			if b == '\\' {
				if err := s.add(2); err != nil {
					return err
				}
				i++
				continue
			}
			if b == '"' {
				inString = false
			}
			n := 1
			if b == '<' || b == '>' || b == '&' {
				n = 6
			}
			if b == 0xe2 && i+2 < len(data) && data[i+1] == 0x80 && (data[i+2] == 0xa8 || data[i+2] == 0xa9) {
				n = 6
				i += 2
			}
			if err := s.add(n); err != nil {
				return err
			}
			continue
		}
		switch b {
		case ' ', '\n', '\t', '\r':
			continue
		case '"':
			inString = true
		case '{', '[':
			depth++
			next := i + 1
			for next < len(data) && strings.ContainsRune(" \r\n\t", rune(data[next])) {
				next++
			}
			if next < len(data) && data[next] != '}' && data[next] != ']' {
				if err := s.line(depth); err != nil {
					return err
				}
			}
		case '}', ']':
			prev := i - 1
			for prev >= 0 && strings.ContainsRune(" \r\n\t", rune(data[prev])) {
				prev--
			}
			depth--
			if prev >= 0 && data[prev] != '{' && data[prev] != '[' {
				if err := s.line(depth); err != nil {
					return err
				}
			}
		case ',':
			if err := s.line(depth); err != nil {
				return err
			}
		case ':':
			if s.indent {
				if err := s.add(1); err != nil {
					return err
				}
			}
		}
		if err := s.add(1); err != nil {
			return err
		}
	}
	return nil
}
