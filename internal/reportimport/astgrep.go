package reportimport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

const astSchema = "https://github.com/ast-grep/ast-grep/blob/main/crates/cli/src/print/json_print.rs"

func importAst(ctx context.Context, data []byte, l Limits, r *Report) error {
	r.Origin.SchemaReference = astSchema
	r.Coordinates = "zero-based Unicode scalar line columns; byteOffset half-open; no conversion"
	r.Limitations = append(r.Limitations, "ast-grep CLI matches do not identify the producer version or run query; both remain unknown.")
	guard := jsonGuard{ctx: ctx, limits: l}
	trim := bytes.TrimSpace(data)
	var records []json.RawMessage
	if len(trim) > 0 && trim[0] == '[' {
		if err := guard.check(trim); err != nil {
			return err
		}
		if err := json.Unmarshal(trim, &records); err != nil {
			return err
		}
	} else {
		// CLI --json=stream emits one complete object per nonempty line.
		for len(data) > 0 {
			line, rest, found := bytes.Cut(data, []byte{'\n'})
			data = rest
			line = bytes.TrimSpace(line)
			if len(line) > 0 {
				if err := guard.check(line); err != nil {
					return err
				}
				if line[0] != '{' {
					return fmt.Errorf("expected ast-grep array or object-per-line JSON stream")
				}
				records = append(records, compact(line))
			}
			if !found {
				break
			}
		}
	}
	if records == nil {
		records = []json.RawMessage{}
	}
	for i, record := range records {
		o, err := object(record)
		if err != nil {
			return err
		}
		path, err := requiredString(o, "file")
		if err != nil {
			return err
		}
		if err := validPath(path, false, l); err != nil {
			return err
		}
		if _, err := requiredString(o, "text"); err != nil {
			return err
		}
		if _, err := requiredString(o, "lines"); err != nil {
			return err
		}
		if language, err := requiredString(o, "language"); err != nil || language == "" {
			return fmt.Errorf("missing ast-grep language")
		}
		rangeData, err := astRange(o["range"])
		if err != nil {
			return fmt.Errorf("match %d range: %w", i, err)
		}
		if err := validateAstNested(o); err != nil {
			return err
		}
		r.Views = append(r.Views, View{Path: path, Ordinal: i, Range: rangeData})
	}
	r.Import.Records = len(records)
	if err := CheckJSONSize(ctx, records, l.OutputBytes, false); err != nil {
		return err
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		return err
	}
	r.Evidence = compact(encoded)
	return nil
}

func astRange(data json.RawMessage) ([]int64, error) {
	o, err := object(data)
	if err != nil {
		return nil, err
	}
	start, err := point(o["start"])
	if err != nil {
		return nil, err
	}
	end, err := point(o["end"])
	if err != nil {
		return nil, err
	}
	if err := validRange(start, end); err != nil {
		return nil, err
	}
	bytesRange, err := object(o["byteOffset"])
	if err != nil {
		return nil, err
	}
	from, err := integer(bytesRange, "start")
	if err != nil {
		return nil, err
	}
	to, err := integer(bytesRange, "end")
	if err != nil || to < from {
		return nil, fmt.Errorf("invalid byteOffset range")
	}
	return append(start, end...), nil
}
