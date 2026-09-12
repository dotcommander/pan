package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/scan"
)

func validateDetail(detail detailLevel) error {
	if detail == "" {
		return nil
	}
	if !detail.valid() {
		return fmt.Errorf("invalid --detail %q (want compact, evidence, or paths)", detail)
	}
	return nil
}

// projectRiskReport applies the output projection after all selectors and
// scoring have completed. JSON round-tripping keeps the wire shape owned by
// scan.RiskReport while allowing compact/path views to omit evidence safely.
func projectRiskReport(report scan.RiskReport, detail detailLevel) any {
	detail = normalizeDetail(detail)
	if detail == detailEvidence {
		return struct {
			scan.RiskReport
			Detail        detailLevel `json:"detail"`
			OmittedFields []string    `json:"omitted_fields"`
		}{report, detail, sortedStrings(nil)}
	}
	if detail == detailPaths {
		paths := make([]string, 0, len(report.Files))
		for _, item := range report.Files {
			paths = append(paths, item.Path)
		}
		return struct {
			Detail             detailLevel       `json:"detail"`
			OmittedFields      []string          `json:"omitted_fields"`
			Paths              []string          `json:"paths"`
			Coverage           scan.RiskCoverage `json:"coverage"`
			FilesOmittedReason string            `json:"files_omitted_reason"`
		}{detail, sortedStrings([]string{"files", "lanes"}), paths, report.Analysis, report.FilesOmittedReason}
	}
	data, err := json.Marshal(report)
	if err != nil {
		return report
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return report
	}
	omitted := []string{"files[].score_components", "lanes"}
	if files, ok := result["files"].([]any); ok {
		for _, item := range files {
			if file, ok := item.(map[string]any); ok {
				if _, exists := file["score_components"]; exists {
					delete(file, "score_components")
				}
			}
		}
	}
	delete(result, "lanes")
	result["detail"] = detail
	result["omitted_fields"] = sortedStrings(omitted)
	return result
}

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiCyan   = "\x1b[36m"
	ansiGreen  = "\x1b[32m"
	ansiAmber  = "\x1b[33m"
	ansiRed    = "\x1b[31m"
	ansiViolet = "\x1b[35m"
)

type terminalStyle struct{ enabled bool }

func (s terminalStyle) paint(code, text string) string {
	if !s.enabled {
		return text
	}
	return code + text + ansiReset
}

func styleFor(w io.Writer) terminalStyle {
	return terminalStyle{enabled: isTerminalWriter(w) && os.Getenv("NO_COLOR") == ""}
}

func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func panHelpPrinter(options kong.HelpOptions, ctx *kong.Context) error {
	if ctx.Selected() != nil {
		return kong.DefaultHelpPrinter(options, ctx)
	}
	return writeRootHelp(ctx.Stdout, styleFor(ctx.Stdout))
}

func writeRootHelp(w io.Writer, style terminalStyle) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nRepository intelligence for coding agents.\n\n", style.paint(ansiBold+ansiCyan, "PAN"))
	b.WriteString("Usage: pan [command] [target] [flags]\n\n")
	b.WriteString(style.paint(ansiBold, "Understand") + "\n")
	b.WriteString("  context    Find and explain relevant code\n")
	b.WriteString("  flow       Trace execution, dependencies, routes, and pipelines\n\n")
	b.WriteString(style.paint(ansiBold, "Evaluate") + "\n")
	b.WriteString("  scan       Inspect repository structure and risk signals\n")
	b.WriteString("  review     Build evidence-backed review packets and reports\n")
	b.WriteString("  scan doctor  Check whether Pan can fully understand this repository\n\n")
	b.WriteString(style.paint(ansiBold, "Improve") + "\n")
	b.WriteString("  clean      Plan repository cleanup; apply only with confirmation\n")
	b.WriteString("  improve    Recommend and validate guarded improvements\n\n")
	b.WriteString(style.paint(ansiDim, "Run `pan` for a repository dashboard, `pan commands` for every command, or `pan <command> --help` for details.") + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func writeCommandCatalog(w io.Writer, app *kong.Application) error {
	nodes := app.Leaves(true)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path() < nodes[j].Path() })
	var b strings.Builder
	b.WriteString("Pan command catalog\n\n")
	for _, node := range nodes {
		fmt.Fprintf(&b, "  %-34s %s\n", node.Path(), node.Help)
	}
	b.WriteString("\nRun `pan <command> --help` for flags and examples.\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func writeHome(w io.Writer, root string) error {
	s := styleFor(w)
	name := filepath.Base(filepath.Clean(root))
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "this repository"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s / %s\n", s.paint(ansiBold+ansiCyan, "PAN"), s.paint(ansiBold, name))
	b.WriteString("  Fast repository intelligence for coding agents\n\n")
	b.WriteString(s.paint(ansiBold, "  START HERE") + "\n")
	b.WriteString("  pan review brief       inspect the repository before a review\n")
	b.WriteString("  pan scan risks         see the highest-signal review targets\n")
	b.WriteString("  pan scan overview      measure repository shape\n")
	b.WriteString("  pan flow storyboard    see how the repository fits together\n")
	b.WriteString("  pan context brief      gather context for the task at hand\n\n")
	b.WriteString(s.paint(ansiDim, "  Tip: `pan commands` shows the complete command catalog.") + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func writeOverview(w io.Writer, snap analyze.Snapshot, report scan.OverviewReport) error {
	s := styleFor(w)
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s  %s\n\n", s.paint(ansiBold+ansiCyan, "REPOSITORY"), s.paint(ansiBold, filepath.Base(snap.Root)))
	fmt.Fprintf(&b, "  %d files   %d symbols   %d edges\n", report.Files, report.Symbols, report.Edges)
	fmt.Fprintf(&b, "  %d tests   %d generated   %d Go packages\n\n", report.TestFiles, report.GeneratedFiles, len(report.GoPackages))
	if len(report.Languages) > 0 {
		b.WriteString(s.paint(ansiBold, "  LANGUAGES") + "\n")
		for _, language := range report.Languages {
			fmt.Fprintf(&b, "  %-16s %d\n", language.Language, language.Files)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "  %s\n", s.paint(ansiDim, analysisSummary(snap)))
	b.WriteString("  → pan scan risks\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func writeRisks(w io.Writer, snap analyze.Snapshot, report scan.RiskReport, detail detailLevel) error {
	if detail == detailPaths {
		for _, item := range report.Files {
			if _, err := fmt.Fprintln(w, item.Path); err != nil {
				return err
			}
		}
		return nil
	}
	s := styleFor(w)
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s", s.paint(ansiBold+ansiCyan, "REVIEW QUEUE"))
	if report.FilesOmittedReason != "" {
		fmt.Fprintf(&b, "  %s", s.paint(ansiDim, report.FilesOmittedReason))
	}
	b.WriteString("\n\n")
	if len(report.Files) == 0 {
		b.WriteString("  ✓ No files scored above the review threshold.\n")
	} else {
		for i, item := range report.Files {
			level, color := riskLevel(item.ReviewPriority)
			fmt.Fprintf(&b, "  %d  %s\n", i+1, s.paint(ansiBold+ansiViolet, item.Path))
			fmt.Fprintf(&b, "     %s review priority · %d", s.paint(color, level), item.ReviewPriority)
			if len(item.Lanes) > 0 {
				fmt.Fprintf(&b, " · %s", strings.Join(item.Lanes, ", "))
			}
			b.WriteString("\n")
			if len(item.Reasons) > 0 {
				fmt.Fprintf(&b, "     %s\n", item.Reasons[0])
			}
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "  Coverage: %d/%d eligible · %d scored · %d returned\n", report.Analysis.EligibleFiles, report.Analysis.SnapshotFiles, report.Analysis.ScoredFiles, report.Analysis.ReturnedFiles)
	if len(report.Analysis.Limits) > 0 {
		fmt.Fprintf(&b, "  Limits: %s\n", strings.Join(report.Analysis.Limits, ", "))
	}
	fmt.Fprintf(&b, "  %s\n", s.paint(ansiDim, analysisSummary(snap)))
	b.WriteString("  → pan review risks\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func writeDoctor(w io.Writer, snap analyze.Snapshot, report scan.DoctorReport) error {
	s := styleFor(w)
	ok := report.Status == "ok"
	marker, status, color := "✓", "READY", ansiGreen
	if !ok {
		marker, status, color = "▲", "DEGRADED", ansiAmber
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s  %s %s\n\n", s.paint(ansiBold+ansiCyan, "PAN DOCTOR"), s.paint(color, marker), s.paint(ansiBold+color, status))
	fmt.Fprintf(&b, "  Analysis   %s\n", boolStatus(s, report.Analysis.Complete))
	fmt.Fprintf(&b, "  Git        %s\n", boolStatus(s, report.Git.Available))
	fmt.Fprintf(&b, "  Config     %s\n", report.Config.Source)
	fmt.Fprintf(&b, "  Evidence   %d files · %d symbols · %d edges\n", report.Analysis.Files, report.Analysis.Symbols, report.Analysis.Edges)
	for _, diagnostic := range report.Analysis.DiagnosticDetails {
		location := ""
		if diagnostic.Location != nil {
			location = fmt.Sprintf(" %s:%d", diagnostic.Location.Path, diagnostic.Location.Line)
		}
		fmt.Fprintf(&b, "  Diagnostic [%s]%s %s\n", diagnostic.Level, location, diagnostic.Message)
	}
	if report.Provider != nil {
		fmt.Fprintf(&b, "  Provider   %s\n", report.Provider.Status)
	}
	if len(report.Warnings) > 0 {
		b.WriteString("\n" + s.paint(ansiBold+ansiAmber, "  ATTENTION") + "\n")
		for _, warning := range report.Warnings {
			fmt.Fprintf(&b, "  ▲ %s\n", warning)
		}
	}
	fmt.Fprintf(&b, "\n  %s\n", s.paint(ansiDim, analysisSummary(snap)))
	_, err := io.WriteString(w, b.String())
	return err
}

func riskLevel(score int) (string, string) {
	switch {
	case score >= 30:
		return "HIGH", ansiRed
	case score >= 15:
		return "MEDIUM", ansiAmber
	default:
		return "LOW", ansiGreen
	}
}

func boolStatus(s terminalStyle, ok bool) string {
	if ok {
		return s.paint(ansiGreen, "✓ available")
	}
	return s.paint(ansiAmber, "▲ limited")
}

func analysisSummary(snap analyze.Snapshot) string {
	if snap.Status.Complete {
		return "Complete analysis"
	}
	if len(snap.Status.Limits) == 0 {
		return "Limited analysis"
	}
	return fmt.Sprintf("Limited analysis · %d configured bound(s) reached", len(snap.Status.Limits))
}
