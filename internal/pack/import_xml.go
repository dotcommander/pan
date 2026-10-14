package pack

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

func (p *imported) readXML(ctx context.Context, raw []byte, l Limits) error {
	d := xml.NewDecoder(bytes.NewReader(raw))
	seenRoot, closedRoot := false, false
	seen := make(map[string]bool)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := d.InputOffset()
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch t := t.(type) {
		case xml.ProcInst:
			if seenRoot || start != 0 || t.Target != "xml" {
				return fmt.Errorf("XML processing instructions are unsupported")
			}
		case xml.Directive:
			return fmt.Errorf("XML directives and entities are unsupported")
		case xml.Comment:
			return fmt.Errorf("XML comments are unsupported")
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return fmt.Errorf("text outside XML containers")
			}
		case xml.StartElement:
			if !seenRoot {
				if t.Name.Space != "" || t.Name.Local != "repomix" || len(t.Attr) != 0 {
					return fmt.Errorf("expected single attribute-free repomix root")
				}
				seenRoot = true
				continue
			}
			if closedRoot || t.Name.Space != "" || len(t.Attr) != 0 {
				return fmt.Errorf("invalid XML container")
			}
			name := t.Name.Local
			if seen[name] {
				return fmt.Errorf("duplicate XML container %q", name)
			}
			seen[name] = true
			switch name {
			case "files":
				if err := p.xmlFiles(d, l); err != nil {
					return err
				}
			case "directory_structure":
				if _, err := xmlText(d, t); err != nil {
					return err
				}
				p.omitted = append(p.omitted, name)
			case "file_summary":
				if err := p.xmlSummary(d, t, raw, l); err != nil {
					return err
				}
			case "user_provided_header", "instruction", "git_diffs", "git_logs":
				if err := xmlMetadata(d, t, 1, l.Depth); err != nil {
					return err
				}
				encoded, err := json.Marshal(string(raw[start:d.InputOffset()]))
				if err != nil {
					return err
				}
				p.metadata[name] = encoded
			default:
				return fmt.Errorf("unsupported Repomix XML field %q", name)
			}
		case xml.EndElement:
			if !seenRoot || closedRoot || t.Name.Local != "repomix" {
				return fmt.Errorf("unexpected XML closing element")
			}
			closedRoot = true
		}
	}
	if !seenRoot || !closedRoot || !seen["files"] {
		return fmt.Errorf("Repomix XML requires repomix/files")
	}
	return nil
}

func (p *imported) xmlFiles(d *xml.Decoder, l Limits) error {
	p.omitted = append(p.omitted, "files.wrapper_text")
	for {
		t, err := d.Token()
		if err != nil {
			return err
		}
		switch t := t.(type) {
		case xml.CharData: // Repomix emits explanatory text before its file records.
		case xml.StartElement:
			if t.Name.Space != "" || t.Name.Local != "file" || len(t.Attr) != 1 || t.Attr[0].Name.Space != "" || t.Attr[0].Name.Local != "path" {
				return fmt.Errorf("expected file with one path attribute")
			}
			name := t.Attr[0].Value
			if _, exists := p.files[name]; exists {
				return fmt.Errorf("duplicate packed path %q", name)
			}
			if len(p.files) >= l.Files {
				return fmt.Errorf("pack exceeds file count limit %d", l.Files)
			}
			content, err := xmlText(d, t)
			if err != nil {
				return err
			}
			p.files[name] = content
		case xml.EndElement:
			return nil
		default:
			return fmt.Errorf("unsupported token in XML files")
		}
	}
}

func xmlText(d *xml.Decoder, start xml.StartElement) (string, error) {
	var text strings.Builder
	for {
		t, err := d.Token()
		if err != nil {
			return "", err
		}
		switch t := t.(type) {
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			if t.Name == start.Name {
				return text.String(), nil
			}
			return "", fmt.Errorf("unexpected XML end")
		default:
			return "", fmt.Errorf("nested markup or directive in XML text")
		}
	}
}
