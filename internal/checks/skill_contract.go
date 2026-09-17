package checks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxSkillNameLength = 64

var (
	namePattern   = regexp.MustCompile(`^[a-z0-9-]+$`)
	numberPattern = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	fencePattern  = regexp.MustCompile(`^[ \t]*(?:(?:[-+*]|\d+[.)])[ \t]+)?(` + "`" + `{3,}|~{3,})(.*)$`)
	todoPattern   = regexp.MustCompile(`^[ ]{0,3}\[TODO:[^\n]*\][ \t]*$`)
	errNotMapping = errors.New("frontmatter must be a YAML dictionary")
)

type skillContractProvider struct{}

func (skillContractProvider) Descriptor() Descriptor {
	return Descriptor{ID: "skill-contract", Description: "Validate the portable SKILL.md frontmatter and unfinished-placeholder contract."}
}

func (skillContractProvider) Applicable(target string, info os.FileInfo) (bool, string, error) {
	if info.IsDir() {
		_, err := os.Stat(filepath.Join(target, "SKILL.md"))
		if os.IsNotExist(err) {
			return false, "target directory does not contain SKILL.md", nil
		}
		return err == nil, "", err
	}
	if filepath.Base(target) != "SKILL.md" {
		return false, "target file is not SKILL.md", nil
	}
	return true, "", nil
}

func (skillContractProvider) Run(_ context.Context, root, target string, info os.FileInfo) (CheckResult, error) {
	path := target
	if info.IsDir() {
		path = filepath.Join(target, "SKILL.md")
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return CheckResult{}, fmt.Errorf("resolve SKILL.md: %w", err)
	}
	rel, err := filepath.Rel(root, resolvedPath)
	if err != nil {
		return CheckResult{}, fmt.Errorf("relativize SKILL.md: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return CheckResult{}, errors.New("SKILL.md resolves outside repository root")
	}
	fileInfo, err := os.Stat(resolvedPath)
	if err != nil {
		return CheckResult{}, fmt.Errorf("stat SKILL.md: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return CheckResult{}, errors.New("SKILL.md must be a regular file")
	}
	data, err := readBounded(resolvedPath, maxSkillBytes)
	if err != nil {
		return CheckResult{}, err
	}
	finding := validateSkill(string(data), filepath.ToSlash(rel))
	if finding == nil {
		return CheckResult{Status: StatusPassed, Findings: []Finding{}}, nil
	}
	return CheckResult{Status: StatusFailed, Findings: []Finding{*finding}}, nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds %d-byte check limit", path, limit)
	}
	return data, nil
}

func validateSkill(content, path string) *Finding {
	fail := func(code string, line int, message string) *Finding {
		return &Finding{Code: code, Severity: "error", Path: path, Line: line, Message: message}
	}
	if !strings.HasPrefix(content, "---") {
		return fail("skill.frontmatter.missing", 1, "No YAML frontmatter found")
	}
	frontmatter, body, bodyLine, ok := splitFrontmatter(content)
	if !ok {
		return fail("skill.frontmatter.format", 1, "Invalid frontmatter format")
	}
	values, lines, err := parseFrontmatter(frontmatter)
	if err != nil {
		if errors.Is(err, errNotMapping) {
			return fail("skill.frontmatter.mapping", 2, "Frontmatter must be a YAML dictionary")
		}
		return fail("skill.frontmatter.yaml", 2, "Invalid YAML in frontmatter: "+err.Error())
	}
	allowed := map[string]bool{"name": true, "description": true, "license": true, "allowed-tools": true, "metadata": true}
	var unexpected []string
	for key := range values {
		if !allowed[key] {
			unexpected = append(unexpected, key)
		}
	}
	if len(unexpected) != 0 {
		sort.Strings(unexpected)
		return fail("skill.frontmatter.unexpected-key", lines[unexpected[0]], "Unexpected key(s) in SKILL.md frontmatter: "+strings.Join(unexpected, ", ")+". Allowed properties are: allowed-tools, description, license, metadata, name")
	}
	name, present := values["name"]
	if !present {
		return fail("skill.name.missing", 2, "Missing 'name' in frontmatter")
	}
	nameLine := lines["name"]
	if name.kind != scalarKind {
		return fail("skill.name.type", nameLine, "Name must be a string, got "+name.kind.String())
	}
	name.value = strings.TrimSpace(name.value)
	if name.value == "" {
		return fail("skill.name.empty", nameLine, "Name must not be empty")
	}
	if !namePattern.MatchString(name.value) {
		return fail("skill.name.format", nameLine, fmt.Sprintf("Name '%s' should be hyphen-case (lowercase letters, digits, and hyphens only)", name.value))
	}
	if strings.HasPrefix(name.value, "-") || strings.HasSuffix(name.value, "-") || strings.Contains(name.value, "--") {
		return fail("skill.name.hyphens", nameLine, fmt.Sprintf("Name '%s' cannot start/end with hyphen or contain consecutive hyphens", name.value))
	}
	if length := utf8.RuneCountInString(name.value); length > maxSkillNameLength {
		return fail("skill.name.length", nameLine, fmt.Sprintf("Name is too long (%d characters). Maximum is %d characters.", length, maxSkillNameLength))
	}
	description, present := values["description"]
	if !present {
		return fail("skill.description.missing", 2, "Missing 'description' in frontmatter")
	}
	descriptionLine := lines["description"]
	if description.kind != scalarKind {
		return fail("skill.description.type", descriptionLine, "Description must be a string, got "+description.kind.String())
	}
	description.value = strings.TrimSpace(description.value)
	if description.value == "" {
		return fail("skill.description.empty", descriptionLine, "Description must not be empty")
	}
	if strings.HasPrefix(description.value, "[TODO:") {
		return fail("skill.description.todo", descriptionLine, "Description contains an unfinished TODO placeholder")
	}
	if strings.ContainsAny(description.value, "<>") {
		return fail("skill.description.angle-bracket", descriptionLine, "Description cannot contain angle brackets (< or >)")
	}
	if length := utf8.RuneCountInString(description.value); length > 1024 {
		return fail("skill.description.length", descriptionLine, fmt.Sprintf("Description is too long (%d characters). Maximum is 1024 characters.", length))
	}
	if line := unfinishedTODOLine(body); line != 0 {
		return fail("skill.body.todo", bodyLine+line-1, "Skill instructions contain an unfinished TODO placeholder")
	}
	return nil
}

func splitFrontmatter(content string) (frontmatter, body string, bodyLine int, ok bool) {
	if !strings.HasPrefix(content, "---\n") {
		return "", "", 0, false
	}
	rest := content[4:]
	index := strings.Index(rest, "\n---")
	if index < 0 {
		return "", "", 0, false
	}
	end := index + 4
	if end < len(rest) && rest[end] != '\n' && rest[end] != '\r' {
		return "", "", 0, false
	}
	frontmatter = rest[:index]
	body = rest[end:]
	bodyLine = 2 + strings.Count(frontmatter, "\n") + 1
	return frontmatter, body, bodyLine, true
}

type valueKind uint8

const (
	scalarKind valueKind = iota
	mapKind
	listKind
	intKind
	floatKind
	boolKind
	noneKind
)

func (k valueKind) String() string {
	switch k {
	case mapKind:
		return "dict"
	case listKind:
		return "list"
	case intKind:
		return "int"
	case floatKind:
		return "float"
	case boolKind:
		return "bool"
	case noneKind:
		return "NoneType"
	default:
		return "string"
	}
}

type frontmatterValue struct {
	kind  valueKind
	value string
}

func parseFrontmatter(frontmatter string) (map[string]frontmatterValue, map[string]int, error) {
	values := map[string]frontmatterValue{}
	keyLines := map[string]int{}
	lines := strings.Split(frontmatter, "\n")
	for lineIndex := 0; lineIndex < len(lines); lineIndex++ {
		raw := lines[lineIndex]
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		if strings.Contains(raw, "\t") {
			return nil, nil, fmt.Errorf("tabs are not permitted")
		}
		if len(raw) > 0 && raw[0] == ' ' {
			continue
		}
		if len(values) == 0 && strings.TrimSpace(raw) == "{}" {
			return values, keyLines, nil
		}
		if len(values) == 0 && strings.HasPrefix(raw, "-") {
			return nil, nil, errNotMapping
		}
		colon := strings.IndexByte(raw, ':')
		if colon <= 0 {
			if len(values) == 0 {
				return nil, nil, errNotMapping
			}
			return nil, nil, fmt.Errorf("expected mapping entry")
		}
		key := strings.TrimSpace(raw[:colon])
		if key == "" {
			return nil, nil, fmt.Errorf("empty mapping key")
		}
		if _, found := values[key]; found {
			return nil, nil, fmt.Errorf("duplicate mapping key %q", key)
		}
		keyLines[key] = lineIndex + 2
		rawValue := strings.TrimSpace(raw[colon+1:])
		value := frontmatterValue{kind: scalarKind, value: rawValue}
		if isBlockScalarHeader(rawValue) {
			blockStart := lineIndex + 1
			blockEnd := blockStart
			for blockEnd < len(lines) && (strings.TrimSpace(lines[blockEnd]) == "" || lines[blockEnd][0] == ' ') {
				blockEnd++
			}
			value.value = parseBlockScalar(rawValue[0], lines[blockStart:blockEnd])
			lineIndex = blockEnd - 1
		} else if rawValue == "" {
			value.kind = mapKind
		} else if strings.HasPrefix(rawValue, "[") {
			value.kind = listKind
		} else if strings.HasPrefix(rawValue, "{") {
			value.kind = mapKind
		} else if strings.HasPrefix(rawValue, "\"") || strings.HasPrefix(rawValue, "'") {
			if len(rawValue) < 2 || rawValue[len(rawValue)-1] != rawValue[0] {
				return nil, nil, fmt.Errorf("unterminated quoted scalar")
			}
			value.value = rawValue[1 : len(rawValue)-1]
		} else if rawValue == "true" || rawValue == "false" {
			value.kind = boolKind
		} else if rawValue == "null" || rawValue == "Null" || rawValue == "NULL" || rawValue == "~" {
			value.kind = noneKind
		} else if numberPattern.MatchString(rawValue) {
			if strings.Contains(rawValue, ".") {
				value.kind = floatKind
			} else {
				value.kind = intKind
			}
		}
		values[key] = value
	}
	if len(values) == 0 {
		return nil, nil, errNotMapping
	}
	return values, keyLines, nil
}

func isBlockScalarHeader(value string) bool {
	if value == "" || (value[0] != '>' && value[0] != '|') {
		return false
	}
	for _, modifier := range value[1:] {
		if modifier != '+' && modifier != '-' && (modifier < '1' || modifier > '9') {
			return false
		}
	}
	return true
}

func parseBlockScalar(style byte, lines []string) string {
	indent := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		current := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 || current < indent {
			indent = current
		}
	}
	stripped := make([]string, len(lines))
	for i, line := range lines {
		if len(line) >= indent {
			stripped[i] = line[indent:]
		}
	}
	if style == '|' {
		return strings.Join(stripped, "\n")
	}
	var result strings.Builder
	for i, line := range stripped {
		if i > 0 {
			if line == "" || stripped[i-1] == "" {
				result.WriteByte('\n')
			} else {
				result.WriteByte(' ')
			}
		}
		result.WriteString(line)
	}
	return result.String()
}

func unfinishedTODOLine(body string) int {
	var marker byte
	fenceLength := 0
	for index, line := range strings.Split(body, "\n") {
		match := fencePattern.FindStringSubmatch(line)
		if len(match) != 0 {
			fence := match[1]
			if marker == 0 {
				marker, fenceLength = fence[0], len(fence)
			} else if fence[0] == marker && len(fence) >= fenceLength && strings.TrimSpace(match[2]) == "" {
				marker, fenceLength = 0, 0
			}
			continue
		}
		if marker == 0 && todoPattern.MatchString(line) {
			return index + 1
		}
	}
	return 0
}
