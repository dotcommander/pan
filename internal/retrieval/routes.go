package retrieval

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
)

// RouteRegistration is one HTTP route binding discovered lexically in a Go
// source file: an HTTP method, the path pattern as written, the handler
// identifier text, and the registration framework it was found in.
type RouteRegistration struct {
	Method     string `json:"method"`
	Pattern    string `json:"pattern"`
	Handler    string `json:"handler"`
	Framework  string `json:"framework"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Confidence string `json:"confidence"`
}

// Shared lexical-analysis vocabulary: the snapshot call-edge kind and the
// Go language label.
const (
	edgeKindCalls = "calls"
	languageGo    = "go"
)

// Registration framework names the detectors report.
const (
	frameworkNetHTTP = "net/http"
	frameworkChi     = "chi"
)

// routeDetector pairs one lexical registration detector with the framework
// it recognizes.
type routeDetector struct {
	re        *regexp.Regexp
	framework string
}

// routeDetectors returns the route-registration detectors. net/http captures
// the quoted "METHOD /path" (or bare "/path") literal and the handler
// argument; the chi detector captures the verb method, a "/"-prefixed path,
// and the handler. The "/" prefix gate keeps unrelated same-named
// .Get/.Post methods out.
func routeDetectors() []routeDetector {
	return []routeDetector{
		{
			re:        regexp.MustCompile(`\b(?:\w+\.)?(?:HandleFunc|Handle)\(\s*"([^"]+)"\s*,\s*([A-Za-z_][\w.]*|\bfunc\b)`),
			framework: frameworkNetHTTP,
		},
		{
			re:        regexp.MustCompile(`\b\w+\.(Get|Post|Put|Delete|Patch|Head|Options|Connect|Trace)\(\s*"(/[^"]*)"\s*,\s*([A-Za-z_][\w.]*|\bfunc\b)`),
			framework: frameworkChi,
		},
	}
}

// Routes scans every non-test Go file in the snapshot for route
// registrations. Extraction is line-lexical: no type checking confirms the
// receiver is an HTTP router, so every registration carries the lexical
// confidence label. An empty result is success, not an error.
func Routes(ctx context.Context, snap analyze.Snapshot) ([]RouteRegistration, error) {
	detectors := routeDetectors()
	var out []RouteRegistration
	for _, file := range snap.Files {
		if file.Language != languageGo || isTestFile(file.Path) {
			continue
		}
		registrations, err := extractRoutes(ctx, snap.Root, file.Path, detectors)
		if err != nil {
			return nil, err
		}
		out = append(out, registrations...)
	}
	return out, nil
}

func extractRoutes(ctx context.Context, root, rel string, detectors []routeDetector) ([]RouteRegistration, error) {
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, fmt.Errorf("read %s for routes: %w", rel, err)
	}
	defer func() { _ = file.Close() }()
	var out []RouteRegistration
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSourceLineBytes)
	lineNo := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lineNo++
		line := scanner.Text()
		for _, pattern := range detectors {
			match := pattern.re.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			registration := RouteRegistration{
				Framework:  pattern.framework,
				File:       rel,
				Line:       lineNo,
				Confidence: analyze.ConfidenceLexical,
			}
			switch pattern.framework {
			case frameworkNetHTTP:
				registration.Method, registration.Pattern = splitMethodPath(match[1])
				registration.Handler = handlerDisplay(match[2])
			case frameworkChi:
				registration.Method = strings.ToUpper(match[1])
				registration.Pattern = match[2]
				registration.Handler = handlerDisplay(match[3])
			}
			out = append(out, registration)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s for routes: %w", rel, err)
	}
	return out, nil
}

// splitMethodPath splits a net/http "METHOD /path" literal on the first
// space; a literal without a space is a method-less registration recorded as
// method ANY.
func splitMethodPath(literal string) (method, pattern string) {
	if i := strings.IndexByte(literal, ' '); i >= 0 {
		return literal[:i], literal[i+1:]
	}
	return "ANY", literal
}

// handlerDisplay normalizes a captured handler argument; an inline func
// literal is recorded as "<inline>".
func handlerDisplay(captured string) string {
	if captured == "func" {
		return "<inline>"
	}
	return captured
}

// matchRoutes selects routes for one endpoint query. Exact matches — the
// full "METHOD PATTERN" string or the bare pattern — win; only when none
// match exactly does a substring match over "METHOD PATTERN" apply.
func matchRoutes(routes []RouteRegistration, query string) []RouteRegistration {
	query = strings.TrimSpace(query)
	var exact, partial []RouteRegistration
	for _, route := range routes {
		canonical := route.Method + " " + route.Pattern
		switch {
		case canonical == query || route.Pattern == query:
			exact = append(exact, route)
		case query != "" && strings.Contains(canonical, query):
			partial = append(partial, route)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
}
