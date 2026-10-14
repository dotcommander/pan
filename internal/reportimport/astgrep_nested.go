package reportimport

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func validateAstNested(o map[string]json.RawMessage) error {
	counts, err := object(o["charCount"])
	if err != nil {
		return fmt.Errorf("invalid charCount: %w", err)
	}
	for _, key := range []string{"leading", "trailing"} {
		if _, err := integer(counts, key); err != nil {
			return err
		}
	}
	if raw, ok := o["replacement"]; ok {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil || string(raw) == "null" {
			return fmt.Errorf("invalid replacement")
		}
	}
	if raw, ok := o["replacementOffsets"]; ok {
		p, err := object(raw)
		if err != nil {
			return err
		}
		a, err := integer(p, "start")
		if err != nil {
			return err
		}
		b, err := integer(p, "end")
		if err != nil || b < a {
			return fmt.Errorf("invalid replacement offsets")
		}
	}
	if raw, ok := o["labels"]; ok {
		if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
			return fmt.Errorf("labels must be an array")
		}
		var labels []json.RawMessage
		if err := json.Unmarshal(raw, &labels); err != nil {
			return err
		}
		for _, label := range labels {
			if err := astNode(label); err != nil {
				return err
			}
		}
	}
	if raw, ok := o["metaVariables"]; ok {
		meta, err := object(raw)
		if err != nil {
			return err
		}
		if raw, ok := meta["single"]; ok {
			nodes, err := object(raw)
			if err != nil {
				return err
			}
			for _, node := range nodes {
				if err := astNode(node); err != nil {
					return err
				}
			}
		}
		if raw, ok := meta["multi"]; ok {
			groups, err := object(raw)
			if err != nil {
				return err
			}
			for _, raw := range groups {
				if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
					return fmt.Errorf("multi capture must be an array")
				}
				var nodes []json.RawMessage
				if err := json.Unmarshal(raw, &nodes); err != nil {
					return err
				}
				for _, node := range nodes {
					if err := astNode(node); err != nil {
						return err
					}
				}
			}
		}
		if raw, ok := meta["transformed"]; ok {
			values, err := object(raw)
			if err != nil {
				return err
			}
			for _, raw := range values {
				var text string
				if err := json.Unmarshal(raw, &text); err != nil || string(raw) == "null" {
					return fmt.Errorf("invalid transformed capture")
				}
			}
		}
	}
	return nil
}

func astNode(raw json.RawMessage) error {
	o, err := object(raw)
	if err != nil {
		return err
	}
	if _, err := requiredString(o, "text"); err != nil {
		return err
	}
	_, err = astRange(o["range"])
	return err
}
