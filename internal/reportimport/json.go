package reportimport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// jsonGuard validates all keys before map decoding can discard ambiguity. The
// shared budget counts every value/container, including additive metadata.
type jsonGuard struct {
	ctx    context.Context
	limits Limits
	values int
}

func (g *jsonGuard) check(data []byte) error {
	if err := validSurrogates(data); err != nil {
		return err
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("invalid UTF-8 JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := g.value(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing or malformed JSON")
	}
	return nil
}

func (g *jsonGuard) value(d *json.Decoder, depth int) error {
	if err := g.ctx.Err(); err != nil {
		return err
	}
	if depth > g.limits.Depth {
		return fmt.Errorf("JSON nesting limit exceeded")
	}
	if g.values >= g.limits.Records {
		return fmt.Errorf("aggregate record/value limit exceeded")
	}
	g.values++
	t, err := d.Token()
	if err != nil {
		return fmt.Errorf("malformed JSON: %w", err)
	}
	switch v := t.(type) {
	case string:
		if len(v) > g.limits.StringBytes {
			return fmt.Errorf("JSON string limit exceeded")
		}
	case json.Number:
		if len(v) > g.limits.StringBytes {
			return fmt.Errorf("JSON number limit exceeded")
		}
	case json.Delim:
		switch v {
		case '{':
			keys := map[string]struct{}{}
			for d.More() {
				if len(keys) >= g.limits.Records {
					return fmt.Errorf("JSON key limit exceeded")
				}
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok {
					return fmt.Errorf("invalid object key")
				}
				if len(name) > g.limits.StringBytes {
					return fmt.Errorf("JSON key string limit exceeded")
				}
				if _, ok := keys[name]; ok {
					return fmt.Errorf("duplicate JSON key %q", name)
				}
				keys[name] = struct{}{}
				if err := g.value(d, depth+1); err != nil {
					return err
				}
			}
			if end, err := d.Token(); err != nil || end != json.Delim('}') {
				return fmt.Errorf("unclosed JSON object")
			}
		case '[':
			for d.More() {
				if err := g.value(d, depth+1); err != nil {
					return err
				}
			}
			if end, err := d.Token(); err != nil || end != json.Delim(']') {
				return fmt.Errorf("unclosed JSON array")
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
	}
	return nil
}

func compact(data []byte) json.RawMessage {
	var b bytes.Buffer
	// Callers have already validated complete JSON, retaining numeric lexemes.
	_ = json.Compact(&b, data)
	return b.Bytes()
}

func object(data json.RawMessage) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return nil, fmt.Errorf("expected JSON object")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

func requiredString(obj map[string]json.RawMessage, name string) (string, error) {
	var text string
	data, ok := obj[name]
	if !ok || json.Unmarshal(data, &text) != nil || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return "", fmt.Errorf("missing or invalid %s string", name)
	}
	return text, nil
}

func integer(obj map[string]json.RawMessage, name string) (int64, error) {
	data, ok := obj[name]
	if !ok {
		return 0, fmt.Errorf("missing coordinate %s", name)
	}
	var number json.Number
	if len(data) == 0 || data[0] == '"' || json.Unmarshal(data, &number) != nil {
		return 0, fmt.Errorf("invalid coordinate %s", name)
	}
	n, err := number.Int64()
	if err != nil || n < 0 {
		return 0, fmt.Errorf("negative, fractional or excessive coordinate %s", name)
	}
	return n, nil
}

func point(data json.RawMessage) ([]int64, error) {
	o, err := object(data)
	if err != nil {
		return nil, err
	}
	line, err := integer(o, "line")
	if err != nil {
		return nil, err
	}
	column, err := integer(o, "column")
	if err != nil {
		return nil, err
	}
	return []int64{line, column}, nil
}

func validRange(start, end []int64) error {
	if len(start) != 2 || len(end) != 2 || start[0] < 0 || start[1] < 0 || end[0] < start[0] || end[1] < 0 || (start[0] == end[0] && end[1] < start[1]) {
		return fmt.Errorf("negative or reversed range")
	}
	return nil
}
