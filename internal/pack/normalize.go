package pack

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// normalize never decodes JSON values: number spelling, duplicate body keys,
// object ordering and string escapes survive lexical whitespace removal.
func normalize(content string, depth int) (string, bool, bool) {
	if jsonDepth(content) > depth {
		return content, false, true
	}
	var out bytes.Buffer
	if err := json.Compact(&out, []byte(content)); err != nil {
		return content, false, false
	}
	normalized := out.String()
	return normalized, normalized != content, false
}

func jsonDepth(content string) int {
	level, maxLevel := 0, 0
	quoted, escape := false, false
	for _, c := range content {
		if quoted {
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{', '[':
			level++
			if level > maxLevel {
				maxLevel = level
			}
		case '}', ']':
			level--
		}
	}
	return maxLevel
}

// render compares complete encoded candidates. Sharing must pay for its array,
// indices and field names; an exact dictionary never aliases content by hash.
func render(doc Document) ([]byte, error) {
	inline, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, f := range doc.Files {
		counts[*f.Content]++
	}
	refs := make(map[string]int)
	for i := range doc.Files {
		content := *doc.Files[i].Content
		if _, ok := refs[content]; ok {
			continue
		}
		count := counts[content]
		if count < 2 {
			continue
		}
		encoded, err := json.Marshal(content)
		if err != nil {
			return nil, err
		}
		// Content field versus ref field plus one shared entry and delimiter.
		inlineCost := count * (len(`"content":`) + len(encoded))
		sharedCost := count*(len(`"ref":`)+len(strconv.Itoa(len(doc.Shared)))) + len(encoded) + 1
		if sharedCost >= inlineCost {
			continue
		}
		refs[content] = len(doc.Shared)
		doc.Shared = append(doc.Shared, content)
	}
	if len(doc.Shared) == 0 {
		return inline, nil
	}
	for i, f := range doc.Files {
		if ref, ok := refs[*f.Content]; ok {
			doc.Files[i].Content = nil
			doc.Files[i].Ref = &ref
		}
	}
	shared, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if len(shared) < len(inline) {
		return shared, nil
	}
	return inline, nil
}
