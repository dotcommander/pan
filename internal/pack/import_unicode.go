package pack

import (
	"fmt"
	"strconv"
)

// encoding/json replaces unpaired UTF-16 escapes. Reject them at intake so a
// path or captured body cannot silently change to the replacement character.
func validJSONUnicode(raw []byte) error {
	quoted := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			break
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return fmt.Errorf("incomplete JSON Unicode escape")
		}
		v, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return fmt.Errorf("invalid JSON Unicode escape")
		}
		i += 4
		if v >= 0xdc00 && v <= 0xdfff {
			return fmt.Errorf("unpaired JSON Unicode surrogate")
		}
		if v < 0xd800 || v > 0xdbff {
			continue
		}
		if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return fmt.Errorf("unpaired JSON Unicode surrogate")
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return fmt.Errorf("unpaired JSON Unicode surrogate")
		}
		i += 6
	}
	return nil
}
