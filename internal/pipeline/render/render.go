// Package render converts Pan specs into self-contained HTML diagrams.
package render

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"path/filepath"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// loadTemplate reads the shared CSS and parses the named template file.
// templateName is both the template.New name and the template definition name.
// templateFile is the path within TemplateFS (e.g. "templates/layout.html").
// sourceRoot is the absolute path used by the absPath template func; "" disables links.
func loadTemplate(templateName, templateFile, sourceRoot string) (*template.Template, error) {
	css, err := TemplateFS.ReadFile("templates/style.css")
	if err != nil {
		return nil, fmt.Errorf("read css: %w", err)
	}
	layout, err := TemplateFS.ReadFile(templateFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", templateFile, err)
	}
	source := strings.Replace(string(layout), "{{.CSS}}", string(css), 1)
	tmpl, err := template.New(templateName).Funcs(funcs(sourceRoot)).Parse(source)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", templateFile, err)
	}
	return tmpl, nil
}

// renderToBytes is the shared template-load + execute path for the layout
// page. Render and ToBytes are thin shims over this helper.
func renderToBytes(s *spec.Spec, sourceRoot string) ([]byte, error) {
	tmpl, err := loadTemplate("layout", "templates/layout.html", sourceRoot)
	if err != nil {
		return nil, err
	}
	data := struct {
		*spec.Spec
		SourceRoot string
	}{Spec: s, SourceRoot: sourceRoot}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}
	return buf.Bytes(), nil
}

// Render writes the spec as self-contained HTML to outPath using an atomic
// temp-file + rename. sourceRoot is the absolute repo root used to build
// vscode:// links; pass "" to disable source links.
func Render(s *spec.Spec, outPath, sourceRoot string) error {
	out, err := renderToBytes(s, sourceRoot)
	if err != nil {
		return err
	}
	return atomicfile.Write(outPath, out, 0o600)
}

// ToBytes renders the spec as self-contained HTML and returns the bytes.
// sourceRoot is the absolute repo root used to build vscode:// links;
// pass "" to disable source links.
func ToBytes(s *spec.Spec, sourceRoot string) ([]byte, error) {
	return renderToBytes(s, sourceRoot)
}

// Index writes the top-level collection index page.
func Index(collections []spec.CollectionSummary, w io.Writer) error {
	tmpl, err := loadTemplate("index", "templates/index.html", "")
	if err != nil {
		return err
	}
	var specCount int
	for _, collection := range collections {
		specCount += len(collection.Specs)
	}
	data := struct {
		Collections []spec.CollectionSummary
		SpecCount   int
	}{Collections: collections, SpecCount: specCount}
	if err := tmpl.ExecuteTemplate(w, "index", data); err != nil {
		return fmt.Errorf("execute index template: %w", err)
	}
	return nil
}

// Collection writes one collection's index page.
func Collection(collection spec.CollectionSummary, w io.Writer) error {
	tmpl, err := loadTemplate("collection", "templates/collection.html", "")
	if err != nil {
		return err
	}
	data := struct{ Collection spec.CollectionSummary }{Collection: collection}
	if err := tmpl.ExecuteTemplate(w, "collection", data); err != nil {
		return fmt.Errorf("execute collection template: %w", err)
	}
	return nil
}

func funcs(sourceRoot string) template.FuncMap {
	return template.FuncMap{
		"inc":        func(i int) int { return i + 1 },
		"joinFiles":  func(ss []string) string { return strings.Join(ss, " · ") },
		"writerVerb": writerVerb,
		"writerNote": writerNote,
		// absPath converts a repo-relative SourceFile into an absolute path for
		// vscode:// links. Returns "" when sourceRoot or relFile is empty.
		"absPath": func(relFile string) string {
			if sourceRoot == "" || relFile == "" {
				return ""
			}
			return filepath.Join(sourceRoot, relFile)
		},
	}
}

func writerVerb(ws []spec.Writer) string {
	if len(ws) == 0 {
		return ""
	}
	switch ws[0].Access {
	case "r/w":
		return "read & written by"
	case "append":
		return "appended by"
	default:
		return "written by"
	}
}

func writerNote(ws []spec.Writer) string {
	for _, writer := range ws {
		if writer.Note != "" {
			return writer.Note
		}
	}
	return ""
}
