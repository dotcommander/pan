package reportimport

import (
	"context"
	"encoding/json"
	"fmt"
)

const cloneSchema = "https://github.com/kucherenko/jscpd/blob/3cc4758c8b2cdb9fb05d19776e4e7d09d94c7988/rust/crates/cpd-reporter/src/json_reporter.rs"

func importClones(ctx context.Context, data []byte, l Limits, r *Report) error {
	guard := jsonGuard{ctx: ctx, limits: l}
	if err := guard.check(data); err != nil {
		return err
	}
	o, err := object(data)
	if err != nil {
		return err
	}
	if _, legacy := o["statistic"]; legacy {
		return fmt.Errorf("legacy jscpd statistic envelope unsupported; use Rust v5 statistics/duplicates JSON report")
	}
	if _, err := object(o["statistics"]); err != nil {
		return fmt.Errorf("jscpd v5 statistics object is required")
	}
	var pairs []json.RawMessage
	if raw, ok := o["duplicates"]; !ok || len(raw) == 0 || raw[0] != '[' {
		return fmt.Errorf("jscpd v5 duplicates array is required")
	} else if err := json.Unmarshal(raw, &pairs); err != nil {
		return err
	}
	for i, pair := range pairs {
		p, err := object(pair)
		if err != nil {
			return err
		}
		for _, name := range []string{"format", "fragment", "kind"} {
			if _, err := requiredString(p, name); err != nil {
				return err
			}
		}
		for _, name := range []string{"lines", "tokens"} {
			if _, err := integer(p, name); err != nil {
				return err
			}
		}
		var isNew bool
		if raw, ok := p["isNew"]; !ok || string(raw) == "null" || json.Unmarshal(raw, &isNew) != nil {
			return fmt.Errorf("missing or invalid clone isNew")
		}
		first, rangeData, err := cloneSide(p["firstFile"], l)
		if err != nil {
			return err
		}
		second, _, err := cloneSide(p["secondFile"], l)
		if err != nil {
			return err
		}
		r.Views = append(r.Views, View{Path: first, OtherPath: second, Ordinal: i, Range: rangeData})
	}
	r.Evidence = compact(data)
	r.Import.Records = len(pairs)
	r.Origin.SchemaReference = cloneSchema
	r.Coordinates = "upstream native line/column/position; column units and base unverified; no conversion"
	r.Limitations = append(r.Limitations, "jscpd Rust v5 JSON schema; exact producer version unknown unless supplied in retained metadata.", "Clones are approximate upstream evidence, not equivalence, deletion or refactoring authority.")
	return nil
}

func cloneSide(raw json.RawMessage, l Limits) (string, []int64, error) {
	o, err := object(raw)
	if err != nil {
		return "", nil, err
	}
	path, err := requiredString(o, "name")
	if err != nil {
		return "", nil, err
	}
	if err := validPath(path, false, l); err != nil {
		return "", nil, err
	}
	startLine, err := integer(o, "start")
	if err != nil {
		return "", nil, err
	}
	endLine, err := integer(o, "end")
	if err != nil || endLine < startLine {
		return "", nil, fmt.Errorf("invalid clone line range")
	}
	start, err := point(o["startLoc"])
	if err != nil {
		return "", nil, err
	}
	end, err := point(o["endLoc"])
	if err != nil {
		return "", nil, err
	}
	if err := validRange(start, end); err != nil {
		return "", nil, err
	}
	if start[0] != startLine || end[0] != endLine {
		return "", nil, fmt.Errorf("clone line and location disagree")
	}
	fromObj, err := object(o["startLoc"])
	if err != nil {
		return "", nil, err
	}
	toObj, err := object(o["endLoc"])
	if err != nil {
		return "", nil, err
	}
	from, err := integer(fromObj, "position")
	if err != nil {
		return "", nil, err
	}
	to, err := integer(toObj, "position")
	if err != nil || to < from {
		return "", nil, fmt.Errorf("invalid clone position range")
	}
	if _, ok := o["nodes"]; ok {
		if _, err := integer(o, "nodes"); err != nil {
			return "", nil, err
		}
	}
	return path, append(start, end...), nil
}
