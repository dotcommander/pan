package render

import "embed"

// TemplateFS embeds the HTML + CSS templates used by renderers.
//
//go:embed templates/*.html templates/*.css
var TemplateFS embed.FS
