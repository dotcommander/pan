package storyboard

import (
	"bytes"
	"html/template"
)

// RenderHTML renders the storyboard as a self-contained dark-theme HTML page.
// The lifecycle-tree body reuses the text renderer so HTML and terminal output
// never drift; review/diff/drift panels are added only when populated.
func RenderHTML(sb Storyboard, opts TextOptions) ([]byte, error) {
	data := htmlData{
		Title:          sb.ProjectName,
		Entrypoint:     sb.Entrypoint,
		ScanWarning:    sb.ScanWarning,
		Body:           string(RenderTextWithOptions(sb, opts)),
		ReviewFindings: sb.ReviewFindings,
		Diffs:          sb.Diffs,
		CommandDrift:   sb.CommandDrift,
	}
	if sb.Coverage != nil {
		data.HasCoverage = true
		data.Represented = sb.Coverage.Represented
		data.Total = sb.Coverage.Total
		data.UnderModeled = len(sb.Coverage.Missing)
	}
	var buf bytes.Buffer
	page, err := htmlTemplate()
	if err != nil {
		return nil, err
	}
	if err := page.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type htmlData struct {
	Title          string
	Entrypoint     string
	ScanWarning    string
	Body           string
	HasCoverage    bool
	Represented    int
	Total          int
	UnderModeled   int
	ReviewFindings []ReviewFinding
	Diffs          []DiffItem
	CommandDrift   []string
}

const htmlPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="generator" content="Pan storyboard">
<title>{{.Title}} storyboard</title>
<style>
:root{color-scheme:dark;--bg:#080a0f;--panel:#0d131d;--panel-2:#111927;--ink:#f3f6fb;--muted:#9aa7b8;--line:#263244;--accent:#62a8ff;--warn:#f2b84b;--ok:#5ec38b;--risk:#ef6f6c;--mono:ui-monospace,SFMono-Regular,Menlo,Consolas,"Liberation Mono",monospace;--sans:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);font:14px/1.5 var(--sans)}
main{width:min(1180px,100%);margin:0 auto;padding:28px 18px 48px}
header{display:flex;align-items:flex-end;justify-content:space-between;gap:16px;padding-bottom:18px;border-bottom:1px solid var(--line)}
h1{margin:0 0 6px;font-size:26px;line-height:1.1}
.path{color:var(--muted);font:13px/1.35 var(--mono);overflow-wrap:anywhere}
.coverage{display:grid;grid-template-columns:repeat(3,minmax(86px,1fr));gap:8px;min-width:300px}
.metric{border:1px solid var(--line);border-radius:6px;padding:10px;background:var(--panel)}
.metric span{display:block;color:var(--muted);font:700 10px/1.2 var(--mono);text-transform:uppercase}
.metric b{display:block;margin-top:4px;font-size:21px;line-height:1}
section{margin-top:18px}
h2{margin:0;font-size:15px;line-height:1.2}
.panel{border:1px solid var(--line);border-radius:8px;background:var(--panel);overflow:hidden}
.panel-title{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:12px 14px;border-bottom:1px solid var(--line);background:var(--panel-2)}
.tag{display:inline-flex;align-items:center;min-height:22px;padding:4px 8px;border:1px solid var(--line);border-radius:999px;color:var(--muted);font:750 10px/1 var(--mono);text-transform:uppercase;white-space:nowrap}
pre{margin:0;padding:16px;overflow:auto;color:var(--ink);font:13px/1.55 var(--mono);white-space:pre}
.warnbar{margin-top:14px;padding:10px 14px;border:1px solid rgba(242,184,75,.75);border-radius:8px;color:#ffe1a3;background:#1a1408;font:13px/1.45 var(--mono)}
ul{margin:0;padding:14px 14px 14px 32px;color:var(--ink)}
li{margin:6px 0;overflow-wrap:anywhere}
code{color:#bed9ff;font-family:var(--mono);overflow-wrap:anywhere}
.sev{display:inline-block;min-width:62px;margin-right:8px;padding:2px 6px;border:1px solid var(--line);border-radius:4px;font:700 10px/1.3 var(--mono);text-transform:uppercase;color:var(--muted)}
.sev-critical,.sev-high{color:#ffd2d1;border-color:rgba(239,111,108,.62)}
.sev-medium{color:#ffe1a3;border-color:rgba(242,184,75,.6)}
@media (max-width:760px){main{padding:18px 12px 36px}header{align-items:stretch;flex-direction:column}.coverage{min-width:0;grid-template-columns:1fr}pre{font-size:12px;padding:12px}}
</style>
</head>
<body>
<main>
<header>
<div>
<h1>{{.Title}} storyboard</h1>
{{if .Entrypoint}}<div class="path">entrypoint: {{.Entrypoint}}</div>{{end}}
</div>
{{if .HasCoverage}}
<div class="coverage" aria-label="Coverage">
<div class="metric"><span>Represented</span><b>{{.Represented}}</b></div>
<div class="metric"><span>Total</span><b>{{.Total}}</b></div>
<div class="metric"><span>Under-modeled</span><b>{{.UnderModeled}}</b></div>
</div>
{{end}}
</header>

{{if .ScanWarning}}<div class="warnbar">{{.ScanWarning}}</div>{{end}}

<section class="panel">
<div class="panel-title"><h2>Lifecycle Storyboard</h2><span class="tag">scan-derived</span></div>
<pre>{{.Body}}</pre>
</section>

{{if .ReviewFindings}}
<section class="panel">
<div class="panel-title"><h2>Review Findings</h2><span class="tag">drift</span></div>
<ul>{{range .ReviewFindings}}<li><span class="sev sev-{{.Severity}}">{{.Severity}}</span><code>{{.Check}}</code> {{.Message}}{{if .Phase}} <code>[{{.Phase}}]</code>{{end}}</li>{{end}}</ul>
</section>
{{end}}

{{if .Diffs}}
<section class="panel">
<div class="panel-title"><h2>Spec Diff</h2><span class="tag">compare</span></div>
<ul>{{range .Diffs}}<li><code>{{.Area}}</code> <span class="sev">{{.Status}}</span> {{.Detail}}</li>{{end}}</ul>
</section>
{{end}}

{{if .CommandDrift}}
<section class="panel">
<div class="panel-title"><h2>Command Drift</h2><span class="tag">cobra vs scan</span></div>
<ul>{{range .CommandDrift}}<li>{{.}}</li>{{end}}</ul>
</section>
{{end}}
</main>
</body>
</html>`

func htmlTemplate() (*template.Template, error) {
	return template.New("storyboard-html").Parse(htmlPage)
}
