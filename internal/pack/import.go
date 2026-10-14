package pack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"unicode"
)

func importPack(ctx context.Context, raw []byte, l Limits) (imported, error) {
	p := imported{files: make(map[string]string), metadata: make(map[string]json.RawMessage)}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return p, fmt.Errorf("empty pack input")
	}
	var err error
	switch trimmed[0] {
	case '{':
		p.format = "repomix_json"
		err = p.readJSON(raw, l)
	case '<':
		p.format = "repomix_parsable_xml"
		err = p.readXML(ctx, raw, l)
	default:
		err = fmt.Errorf("unsupported pack format")
	}
	if err != nil {
		return p, fmt.Errorf("import pack: %w; use repomix --style json or --style xml --parsable-style", err)
	}
	if len(p.files) > l.Files {
		return p, fmt.Errorf("pack exceeds file count limit %d", l.Files)
	}
	paths := make([]string, 0, len(p.files))
	for name := range p.files {
		paths = append(paths, name)
	}
	slices.Sort(paths)
	var decoded int64
	for _, name := range paths {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		if err := validPath(name, l.PathBytes); err != nil {
			return p, err
		}
		length := int64(len(p.files[name]))
		if length > l.FileBytes {
			return p, fmt.Errorf("packed file %q exceeds byte limit %d", name, l.FileBytes)
		}
		decoded += length
		if decoded > l.DecodedBytes {
			return p, fmt.Errorf("pack decoded content exceeds byte limit %d", l.DecodedBytes)
		}
	}
	slices.Sort(p.omitted)
	return p, nil
}

func validPath(name string, max int) error {
	bad := name == "" || len(name) > max || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\:") || path.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, "../")
	for _, c := range name {
		bad = bad || unicode.IsControl(c)
	}
	if bad {
		return fmt.Errorf("unsafe or noncanonical packed path %q", name)
	}
	return nil
}

func (p *imported) readJSON(raw []byte, l Limits) error {
	if err := validJSONUnicode(raw); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := uniqueJSON(d, 0, l, false); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	files, exists := envelope["files"]
	if !exists || len(files) == 0 || files[0] != '{' {
		return fmt.Errorf("Repomix files must be an object")
	}
	if err := json.Unmarshal(files, &p.files); err != nil {
		return fmt.Errorf("Repomix files must map paths to strings: %w", err)
	}
	for key, value := range envelope {
		switch key {
		case "files":
		case "directoryStructure":
			var tree string
			if err := json.Unmarshal(value, &tree); err != nil || bytes.Equal(value, []byte("null")) {
				return fmt.Errorf("directoryStructure must be a string")
			}
			p.omitted = append(p.omitted, "directoryStructure")
		case "fileSummary":
			if len(value) == 0 || value[0] != '{' {
				return fmt.Errorf("fileSummary must be an object")
			}
			var summary map[string]json.RawMessage
			if err := json.Unmarshal(value, &summary); err != nil {
				return err
			}
			for _, generated := range []string{"purpose", "fileFormat", "usageGuidelines"} {
				if _, ok := summary[generated]; ok {
					delete(summary, generated)
					p.omitted = append(p.omitted, "fileSummary."+generated)
				}
			}
			if len(summary) > 0 {
				retained, err := json.Marshal(summary)
				if err != nil {
					return err
				}
				p.metadata[key] = retained
			}
		case "userProvidedHeader", "gitDiffs", "gitLogs", "instruction":
			p.metadata[key] = value
		default:
			return fmt.Errorf("unsupported Repomix field %q", key)
		}
	}
	return nil
}

// uniqueJSON guards the envelope, including duplicate escaped keys, before maps
// can overwrite them. File-body strings remain opaque during envelope parsing.
func uniqueJSON(d *json.Decoder, depth int, limits Limits, files bool) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	if depth >= limits.Depth {
		return fmt.Errorf("pack JSON nesting exceeds limit %d", limits.Depth)
	}
	if files && delim != '{' {
		return fmt.Errorf("Repomix files must be an object")
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("invalid JSON object key")
			}
			if seen[name] {
				return fmt.Errorf("duplicate JSON key %q", name)
			}
			// Bound the first inventory traversal before retaining another key
			// or decoding its potentially large value.
			if files && len(seen) >= limits.Files {
				return fmt.Errorf("pack exceeds file count limit %d", limits.Files)
			}
			seen[name] = true
			if files {
				value, err := d.Token()
				if err != nil {
					return err
				}
				if _, ok := value.(string); !ok {
					return fmt.Errorf("packed file %q must be a string", name)
				}
				continue
			}
			if err := uniqueJSON(d, depth+1, limits, depth == 0 && name == "files"); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueJSON(d, depth+1, limits, false); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	_, err = d.Token()
	return err
}
