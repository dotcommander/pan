package ranking

import (
	"path"
	"strings"
	"unicode"
)

// Term matching expands compound identifiers into sub-tokens and separates
// exact part matches from prefix-overlap matches, so an intent like "parse
// config" matches parseConfig, ConfigParser, and config_parser at full
// strength, morphological variants (dependency/dependencies) at partial
// strength, and incidental substrings (auth/author) never at full strength.

// camelBoundary splits camelCase/PascalCase identifiers on case boundaries,
// keeping acronym runs intact: getHTTPResponse -> get, HTTP, Response;
// HTMLParser -> HTML, Parser; OAuth2 -> O, Auth2.
func camelBoundary(token string) []string {
	runes := []rune(token)
	var parts []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		boundary := unicode.IsUpper(cur) && (unicode.IsLower(prev) || unicode.IsDigit(prev))
		if !boundary && unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
			// last uppercase of an acronym run before a lowercase rune
			boundary = true
		}
		if boundary {
			parts = append(parts, string(runes[start:i]))
			start = i
		}
	}
	if start < len(runes) {
		parts = append(parts, string(runes[start:]))
	}
	return parts
}

// minPrefixRunes is the shortest side allowed for a prefix-overlap match, so
// short substrings cannot dominate ranking through incidental prefixes.
const minPrefixRunes = 3

// CommonStopwords are high-frequency English words excluded from intent and
// goal term extraction.
var CommonStopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "into": {},
	"that": {}, "this": {}, "from": {}, "add": {}, "fix": {},
}

// SplitIdentifier splits one identifier into its lowered compound followed by
// lowered sub-tokens. snake_case splits on underscores; camelCase/PascalCase
// split on case boundaries. Identifiers without boundaries return only the
// lowered compound.
func SplitIdentifier(token string) []string {
	lower := strings.ToLower(token)
	var parts []string
	if strings.Contains(token, "_") {
		for _, part := range strings.Split(lower, "_") {
			if part != "" {
				parts = append(parts, part)
			}
		}
	} else {
		for _, part := range camelBoundary(token) {
			if lowered := strings.ToLower(part); lowered != "" {
				parts = append(parts, lowered)
			}
		}
	}
	if len(parts) < 2 {
		return []string{lower}
	}
	return append([]string{lower}, parts...)
}

// MatchPart reports whether term matches any part and whether the strongest
// match was exact. Exact matches require equality after snake-case
// normalization; prefix matches require the shorter side to be at least
// minPrefixRunes runes and a prefix of the longer side.
func MatchPart(parts []string, term string) (matched, exact bool) {
	normalizedTerm := strings.ReplaceAll(term, "_", "")
	for _, part := range parts {
		if term == part || strings.ReplaceAll(part, "_", "") == normalizedTerm {
			return true, true
		}
	}
	for _, part := range parts {
		shorter, longer := term, part
		if len(shorter) > len(longer) {
			shorter, longer = longer, shorter
		}
		if len(shorter) >= minPrefixRunes && strings.HasPrefix(longer, shorter) {
			return true, false
		}
	}
	return false, false
}

// PathTerms returns the lowered matching vocabulary of one repository path:
// every sub-token of every path segment, with extensions excluded.
func PathTerms(p string) []string {
	clean := strings.TrimPrefix(strings.ReplaceAll(p, `\`, "/"), "./")
	segments := strings.Split(clean, "/")
	terms := make([]string, 0, len(segments)*2)
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			continue
		}
		stem := strings.TrimSuffix(segment, path.Ext(segment))
		if stem == "" {
			continue
		}
		terms = append(terms, SplitIdentifier(stem)...)
	}
	return terms
}

// ExpandTerms tokenizes free text into distinct lowered search terms: raw
// words of at least minRunes runes (minus stopwords), each expanded with its
// identifier sub-tokens so compound queries match compound identifiers.
// Splitting uses the original casing so camelCase boundaries survive.
func ExpandTerms(text string, minRunes int, stopwords map[string]struct{}) []string {
	var terms []string
	seen := make(map[string]struct{})
	add := func(term string) {
		if len([]rune(term)) < minRunes {
			return
		}
		if _, drop := stopwords[term]; drop {
			return
		}
		if _, dup := seen[term]; dup {
			return
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	split := func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }
	for _, field := range strings.FieldsFunc(text, split) {
		add(strings.ToLower(field))
		for _, part := range SplitIdentifier(field) {
			add(part)
		}
	}
	return terms
}
