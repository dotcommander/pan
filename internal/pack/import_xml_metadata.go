package pack

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
)

// Metadata is inert preserved XML, validated before taking its exact raw slice.
func xmlMetadata(d *xml.Decoder, start xml.StartElement, depth, max int) error {
	if depth >= max || start.Name.Space != "" || len(start.Attr) != 0 {
		return fmt.Errorf("invalid or excessive XML metadata nesting")
	}
	seen := make(map[string]bool)
	for {
		t, err := d.Token()
		if err != nil {
			return err
		}
		switch t := t.(type) {
		case xml.CharData:
		case xml.StartElement:
			name := t.Name.Local
			repeated := (start.Name.Local == "git_logs" && name == "git_log_commit") || (start.Name.Local == "git_log_commit" && name == "files")
			if seen[name] && !repeated {
				return fmt.Errorf("duplicate XML metadata field %q", name)
			}
			seen[name] = true
			if err := xmlMetadata(d, t, depth+1, max); err != nil {
				return err
			}
		case xml.EndElement:
			if t.Name == start.Name {
				return nil
			}
			return fmt.Errorf("unexpected XML metadata end")
		default:
			return fmt.Errorf("unsupported XML metadata token")
		}
	}
}

func (p *imported) xmlSummary(d *xml.Decoder, start xml.StartElement, raw []byte, l Limits) error {
	seen := make(map[string]bool)
	var generation strings.Builder
	for {
		offset := d.InputOffset()
		t, err := d.Token()
		if err != nil {
			return err
		}
		switch t := t.(type) {
		case xml.CharData:
			generation.Write(t)
		case xml.StartElement:
			name := t.Name.Local
			if seen[name] {
				return fmt.Errorf("duplicate XML summary field %q", name)
			}
			seen[name] = true
			if err := xmlMetadata(d, t, 1, l.Depth); err != nil {
				return err
			}
			switch name {
			case "purpose", "file_format", "usage_guidelines":
				p.omitted = append(p.omitted, "file_summary."+name)
			case "notes":
				encoded, err := json.Marshal(string(raw[offset:d.InputOffset()]))
				if err != nil {
					return err
				}
				p.metadata["file_summary.notes"] = encoded
			default:
				return fmt.Errorf("unsupported XML summary field %q", name)
			}
		case xml.EndElement:
			if t.Name != start.Name {
				return fmt.Errorf("unexpected XML summary end")
			}
			if text := generation.String(); strings.TrimSpace(text) != "" {
				encoded, err := json.Marshal(text)
				if err != nil {
					return err
				}
				p.metadata["file_summary.generation_header"] = encoded
			}
			return nil
		default:
			return fmt.Errorf("unsupported XML summary token")
		}
	}
}
