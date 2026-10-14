package reportimport

import "fmt"

// encoding/json repairs unpaired UTF-16 escapes. Reject them before decoding so
// identity-bearing strings and object keys cannot silently collapse together.
func validSurrogates(data []byte) error {
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			break
		}
		if data[i] != 'u' {
			continue
		}
		value, ok := hexUnit(data, i+1)
		if !ok {
			continue
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return fmt.Errorf("unpaired low UTF-16 surrogate escape")
		}
		if value < 0xd800 || value > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return fmt.Errorf("unpaired high UTF-16 surrogate escape")
		}
		low, ok := hexUnit(data, i+3)
		if !ok || low < 0xdc00 || low > 0xdfff {
			return fmt.Errorf("unpaired high UTF-16 surrogate escape")
		}
		i += 6
	}
	return nil
}
func hexUnit(data []byte, start int) (uint16, bool) {
	if start+4 > len(data) {
		return 0, false
	}
	var n uint16
	for _, b := range data[start : start+4] {
		n <<= 4
		switch {
		case b >= '0' && b <= '9':
			n += uint16(b - '0')
		case b >= 'a' && b <= 'f':
			n += uint16(b - 'a' + 10)
		case b >= 'A' && b <= 'F':
			n += uint16(b - 'A' + 10)
		default:
			return 0, false
		}
	}
	return n, true
}
