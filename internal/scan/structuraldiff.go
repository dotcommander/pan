package scan

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
)

// Structural-diff change kinds and confidences. This vocabulary is local to
// the diff report; it is deliberately not shared with edge confidence
// labels.
const (
	DiffChangeAdded     = "added"
	DiffChangeRemoved   = "removed"
	DiffChangeBody      = "body"
	DiffChangeSignature = "signature"
	DiffChangeMoved     = "moved"

	DiffFileAdded    = "added"
	DiffFileRemoved  = "removed"
	DiffFileModified = "modified"
	DiffFileRenamed  = "renamed"

	DiffModeWorktree  = "worktree"
	DiffModeCommitted = "committed"

	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
)

// Structural-diff bounds. The git-output cap bounds one range's raw diff;
// per-side file content is capped at diffMaxFileBytes.
const (
	diffOutputCap    = 8 << 20
	diffMaxFileBytes = 1 << 20
	diffDefaultTop   = 50
	diffRangeDots    = ".."
	diffNoRev        = "HEAD"
	diffMaxNotes     = 20
)

// DiffSymbol is one changed top-level symbol: what changed, where it lived
// before and after, and how certain the classification is.
type DiffSymbol struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Change     string `json:"change"`
	BeforeLine int    `json:"before_line,omitempty"`
	AfterLine  int    `json:"after_line,omitempty"`
	MovedFrom  string `json:"moved_from,omitempty"`
	Confidence string `json:"confidence"`
}

// DiffFile is one changed file with its per-symbol overlay.
type DiffFile struct {
	Path    string       `json:"path"`
	Status  string       `json:"status"`
	Symbols []DiffSymbol `json:"symbols,omitempty"`
}

// DiffReport is the symbol-level view over one git range. Every claim is
// derived from parsed hunks plus outlined revisions; files whose content
// could not be outlined appear with a status and no symbols.
type DiffReport struct {
	Range       string               `json:"range"`
	Mode        string               `json:"mode"`
	Base        string               `json:"base"`
	Head        string               `json:"head"`
	Files       []DiffFile           `json:"files"`
	Truncations []analyze.Truncation `json:"truncations,omitempty"`
	Notes       []string             `json:"notes,omitempty"`
}

// DiffOptions controls the structural diff pass.
type DiffOptions struct {
	// Top caps the listed files; zero uses diffDefaultTop.
	Top int
}

// StructuralDiff builds the symbol-level overlay over one git range.
//
// A rev of "" or "HEAD" diffs the working tree against HEAD (mode
// worktree). A rev of the form "A..B" diffs two committed revisions, and a
// single other rev R diffs R..HEAD (mode committed). Content comes from the
// git object store for committed revisions and from bounded working-tree
// reads for the worktree side, so the overlay never assumes the working
// tree matches any revision.
func StructuralDiff(ctx context.Context, root, rev string, options DiffOptions) (DiffReport, error) {
	spec := diffSpec(rev)
	if strings.HasPrefix(spec.head, ".") {
		return DiffReport{Range: rev}, fmt.Errorf("structural diff: %q selects a symmetric A...B range; use A..B to name both revisions", rev)
	}
	if spec.head == "" {
		spec.head = diffNoRev
	}
	report := DiffReport{Range: rev, Mode: spec.mode, Base: spec.base, Head: spec.head}
	if options.Top <= 0 {
		options.Top = diffDefaultTop
	}
	diffArgs := []string{"diff", "--unified=0", "--find-renames", "--no-prefix"}
	if spec.mode == DiffModeWorktree {
		diffArgs = append(diffArgs, spec.base)
	} else {
		diffArgs = append(diffArgs, spec.base+diffRangeDots+spec.head)
	}
	diffArgs = append(diffArgs, "--")
	out, err := readGitBounded(ctx, root, diffOutputCap, diffArgs...)
	if err != nil {
		return report, fmt.Errorf("structural diff: %w", err)
	}
	parsed := parseUnifiedDiff(out)
	files, notes := overlayDiffFiles(ctx, root, spec, parsed)
	if len(files) > options.Top {
		report.Truncations = append(report.Truncations, analyze.Truncation{
			Field: "files", Shown: options.Top, Total: len(files), Reason: "file cap",
		})
		files = files[:options.Top]
	}
	report.Files = files
	report.Notes = boundedNotes(notes)
	return report, nil
}

// diffResolution is one resolved diff target: which two states to compare.
type diffResolution struct {
	mode string
	base string
	head string
}

// diffSpec resolves the user-supplied revision selector.
func diffSpec(rev string) diffResolution {
	trimmed := strings.TrimSpace(rev)
	if trimmed == "" || trimmed == diffNoRev {
		return diffResolution{mode: DiffModeWorktree, base: diffNoRev, head: ""}
	}
	if base, head, found := strings.Cut(trimmed, diffRangeDots); found {
		return diffResolution{mode: DiffModeCommitted, base: base, head: head}
	}
	return diffResolution{mode: DiffModeCommitted, base: trimmed, head: diffNoRev}
}

// fileDiff is one parsed file from `git diff --unified=0` output.
type fileDiff struct {
	oldPath   string
	newPath   string
	status    string
	binary    bool
	oldRanges []lineRange
	newRanges []lineRange
}

// lineRange is one half-open hunk range [start, end) on one side.
type lineRange struct {
	start int
	end   int
}

// parseUnifiedDiff parses `git diff --unified=0 --find-renames` output into
// per-file hunks. Unknown extended headers are ignored; files with no
// hunks (binary files, mode-only changes, pure renames) are kept.
func parseUnifiedDiff(out string) []fileDiff {
	var files []fileDiff
	var current *fileDiff
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			files = append(files, fileDiff{status: DiffFileModified})
			current = &files[len(files)-1]
			current.oldPath, current.newPath = parseDiffGitPaths(strings.TrimPrefix(line, "diff --git "))
		case current == nil:
			continue
		case strings.HasPrefix(line, "new file mode"):
			current.status = DiffFileAdded
		case strings.HasPrefix(line, "deleted file mode"):
			current.status = DiffFileRemoved
		case strings.HasPrefix(line, "rename from "):
			current.oldPath = unquoteGitPath(strings.TrimPrefix(line, "rename from "))
			current.status = DiffFileRenamed
		case strings.HasPrefix(line, "rename to "):
			current.newPath = unquoteGitPath(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "Binary files "):
			current.binary = true
		case strings.HasPrefix(line, "--- "):
			current.oldPath = diffHeaderPath(line[4:])
		case strings.HasPrefix(line, "+++ "):
			current.newPath = diffHeaderPath(line[4:])
		case strings.HasPrefix(line, "@@ "):
			oldRange, newRange, ok := parseHunkHeader(line)
			if ok {
				current.oldRanges = append(current.oldRanges, oldRange)
				current.newRanges = append(current.newRanges, newRange)
			}
		}
	}
	return files
}

// diffHeaderPath resolves one ---/+++ header path. Diff output uses
// --no-prefix, so only /dev/null needs special handling; git C-quotes
// paths that contain spaces or non-ASCII bytes.
func diffHeaderPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "/dev/null" {
		return ""
	}
	return unquoteGitPath(value)
}

// parseDiffGitPaths splits the two paths from a --no-prefix `diff --git`
// line. They are a fallback for files without ---/+++ headers (binary
// files); rename from/to lines override them for renames. Quoted paths
// keep embedded spaces, so simple space-splitting is not enough.
func parseDiffGitPaths(rest string) (string, string) {
	old, tail := cutDiffPathToken(rest)
	new, _ := cutDiffPathToken(tail)
	return old, new
}

// cutDiffPathToken splits one leading path token from rest, honoring a
// git-quoted segment so embedded spaces survive, and returns the token
// plus the remaining text.
func cutDiffPathToken(rest string) (string, string) {
	rest = strings.TrimLeft(rest, " ")
	if strings.HasPrefix(rest, `"`) {
		escaped := false
		for i := 1; i < len(rest); i++ {
			if escaped {
				escaped = false
				continue
			}
			switch rest[i] {
			case '\\':
				escaped = true
			case '"':
				return unquoteGitPath(rest[:i+1]), rest[i+1:]
			}
		}
	}
	token, tail, _ := strings.Cut(rest, " ")
	return token, tail
}

// unquoteGitPath removes git's C-style quoting from one header path. Git
// surrounds paths containing spaces or non-ASCII bytes with double quotes
// and escapes bytes C-style; each octal escape decodes back to one byte,
// which reconstructs the original UTF-8 sequence. Values without quoting
// return unchanged, and unknown escapes are kept literally.
func unquoteGitPath(value string) string {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return value
	}
	body := value[1 : len(value)-1]
	var out strings.Builder
	out.Grow(len(body))
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' || i+1 >= len(body) {
			out.WriteByte(body[i])
			continue
		}
		i++
		switch body[i] {
		case 'a':
			out.WriteByte('\a')
		case 'b':
			out.WriteByte('\b')
		case 'f':
			out.WriteByte('\f')
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		case 'v':
			out.WriteByte('\v')
		case '\\', '"':
			out.WriteByte(body[i])
		default:
			if i+2 < len(body) && isOctalDigit(body[i]) && isOctalDigit(body[i+1]) && isOctalDigit(body[i+2]) {
				out.WriteByte(byte((octalValue(body[i]) << 6) + (octalValue(body[i+1]) << 3) + octalValue(body[i+2])))
				i += 2
			} else {
				out.WriteByte('\\')
				out.WriteByte(body[i])
			}
		}
	}
	return out.String()
}

func isOctalDigit(c byte) bool { return c >= '0' && c <= '7' }

func octalValue(c byte) int { return int(c - '0') }

// parseHunkHeader parses "@@ -a,b +c,d @@ ..." into both half-open ranges.
func parseHunkHeader(line string) (lineRange, lineRange, bool) {
	rest, _, found := strings.Cut(strings.TrimPrefix(line, "@@"), "@@")
	if !found {
		return lineRange{}, lineRange{}, false
	}
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return lineRange{}, lineRange{}, false
	}
	oldRange, okOld := parseCountSpec(fields[0])
	newRange, okNew := parseCountSpec(fields[1])
	if !okOld || !okNew {
		return lineRange{}, lineRange{}, false
	}
	return oldRange, newRange, true
}

// parseCountSpec parses one -start,count or +start spec. A zero count
// becomes a one-line anchor so insertion points still overlap the affected
// region.
func parseCountSpec(spec string) (lineRange, bool) {
	if len(spec) < 2 || (spec[0] != '-' && spec[0] != '+') {
		return lineRange{}, false
	}
	startText, countText, hasCount := strings.Cut(spec[1:], ",")
	start, err := strconv.Atoi(startText)
	if err != nil || start < 0 {
		return lineRange{}, false
	}
	count := 1
	if hasCount {
		count, err = strconv.Atoi(countText)
		if err != nil || count < 0 {
			return lineRange{}, false
		}
	}
	if count == 0 {
		return lineRange{start: start, end: start + 1}, true
	}
	return lineRange{start: start, end: start + count}, true
}

// overlayDiffFiles maps parsed hunks onto outlined symbols for both sides.
func overlayDiffFiles(ctx context.Context, root string, spec diffResolution, files []fileDiff) ([]DiffFile, []string) {
	var notes []string
	results := make([]DiffFile, 0, len(files))
	for _, fd := range files {
		path := fd.newPath
		if path == "" {
			path = fd.oldPath
		}
		if path == "" {
			continue
		}
		result := DiffFile{Path: path, Status: fd.status}
		if fd.binary {
			results = append(results, result)
			continue
		}
		before, hasBefore := revisionContent(ctx, root, spec, fd, true, &notes, path)
		after, hasAfter := revisionContent(ctx, root, spec, fd, false, &notes, path)
		if !hasBefore && !hasAfter {
			results = append(results, result)
			continue
		}
		beforeSymbols, _ := analyze.OutlineSymbols(path, before)
		afterSymbols, _ := analyze.OutlineSymbols(path, after)
		result.Symbols = classifyFile(fd, beforeSymbols, afterSymbols)
		results = append(results, result)
	}
	slices.SortFunc(results, func(a, b DiffFile) int { return strings.Compare(a.Path, b.Path) })
	results, notes = detectCrossFileMoves(results, notes)
	return results, notes
}

// revisionContent reads one side's file content; before selects the base
// side. Absent files return ok=false without a note; oversized or
// unreadable content records one bounded note and returns ok=false.
func revisionContent(ctx context.Context, root string, spec diffResolution, fd fileDiff, before bool, notes *[]string, displayPath string) ([]byte, bool) {
	if before && fd.status == DiffFileAdded {
		return nil, false
	}
	if !before && fd.status == DiffFileRemoved {
		return nil, false
	}
	if spec.mode == DiffModeWorktree && !before {
		return readWorktreeFileBounded(filepath.Join(root, displayPath), displayPath, notes)
	}
	rev := spec.head
	if before {
		rev = spec.base
	}
	objectPath := displayPath
	if before && fd.oldPath != "" {
		objectPath = fd.oldPath
	}
	exists, err := revisionPathExists(ctx, root, rev, objectPath)
	if err != nil {
		*notes = append(*notes, fmt.Sprintf("%s: revision lookup failed: %v", displayPath, err))
		return nil, false
	}
	if !exists {
		return nil, false
	}
	out, err := readGitBounded(ctx, root, diffMaxFileBytes, "show", rev+":"+objectPath)
	if err != nil {
		*notes = append(*notes, fmt.Sprintf("%s: revision read failed: %v", displayPath, err))
		return nil, false
	}
	return []byte(out), true
}

// readWorktreeFileBounded reads at most diffMaxFileBytes bytes of one
// working-tree file so an oversized file is rejected without being fully
// loaded into memory.
func readWorktreeFileBounded(path, displayPath string, notes *[]string) ([]byte, bool) {
	file, err := os.Open(path)
	if err != nil {
		*notes = append(*notes, fmt.Sprintf("%s: working-tree read failed: %v", displayPath, err))
		return nil, false
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, diffMaxFileBytes+1))
	if err != nil {
		*notes = append(*notes, fmt.Sprintf("%s: working-tree read failed: %v", displayPath, err))
		return nil, false
	}
	if len(content) > diffMaxFileBytes {
		*notes = append(*notes, fmt.Sprintf("%s: symbol outline skipped (file exceeds %d bytes)", displayPath, diffMaxFileBytes))
		return nil, false
	}
	return content, true
}

// revisionPathExists reports whether path exists in rev using one
// read-only ls-tree probe.
func revisionPathExists(ctx context.Context, root, rev, path string) (bool, error) {
	out, err := readGitBounded(ctx, root, 4096, "ls-tree", rev, "--", path)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// classifyFile produces the per-symbol overlay for one file. Whole-file
// adds and removes outline every symbol; modified files report only
// symbols whose span overlaps a hunk on the side that still carries them.
func classifyFile(fd fileDiff, before, after []analyze.Symbol) []DiffSymbol {
	switch fd.status {
	case DiffFileAdded:
		return wholeFileSymbols(after, DiffChangeAdded)
	case DiffFileRemoved:
		return wholeFileSymbols(before, DiffChangeRemoved)
	}
	var symbols []DiffSymbol
	afterBy := symbolKeyIndex(before)
	for _, symbol := range after {
		if !overlapsAny(symbolSpan(symbol), fd.newRanges) {
			continue
		}
		prior, found := afterBy[symbol.Name+"#"+symbol.Kind]
		if !found {
			symbols = append(symbols, DiffSymbol{
				Name: symbol.Name, Kind: symbol.Kind, Change: DiffChangeAdded,
				AfterLine: symbol.Location.Line, Confidence: ConfidenceHigh,
			})
			continue
		}
		symbols = append(symbols, changedPair(symbol, prior))
	}
	seen := make(map[string]bool, len(after))
	for _, symbol := range after {
		seen[symbol.Name+"#"+symbol.Kind] = true
	}
	for _, symbol := range before {
		key := symbol.Name + "#" + symbol.Kind
		if seen[key] {
			continue
		}
		if !overlapsAny(symbolSpan(symbol), fd.oldRanges) {
			continue
		}
		symbols = append(symbols, DiffSymbol{
			Name: symbol.Name, Kind: symbol.Kind, Change: DiffChangeRemoved,
			BeforeLine: symbol.Location.Line, Confidence: ConfidenceHigh,
		})
	}
	sortDiffSymbols(symbols)
	return symbols
}

// changedPair classifies one symbol present on both sides whose span
// overlaps a hunk. A differing signature is direct evidence; a body change
// rests on line overlap alone.
func changedPair(after, before analyze.Symbol) DiffSymbol {
	entry := DiffSymbol{
		Name:       after.Name,
		Kind:       after.Kind,
		BeforeLine: before.Location.Line,
		AfterLine:  after.Location.Line,
		Confidence: ConfidenceMedium,
	}
	if after.Signature != before.Signature {
		entry.Change = DiffChangeSignature
		entry.Confidence = ConfidenceHigh
		return entry
	}
	entry.Change = DiffChangeBody
	return entry
}

// wholeFileSymbols marks every symbol in one side of a whole-file add or
// remove.
func wholeFileSymbols(symbols []analyze.Symbol, change string) []DiffSymbol {
	out := make([]DiffSymbol, 0, len(symbols))
	for _, symbol := range symbols {
		entry := DiffSymbol{Name: symbol.Name, Kind: symbol.Kind, Change: change, Confidence: ConfidenceHigh}
		if change == DiffChangeAdded {
			entry.AfterLine = symbol.Location.Line
		} else {
			entry.BeforeLine = symbol.Location.Line
		}
		out = append(out, entry)
	}
	sortDiffSymbols(out)
	return out
}

// moveSite identifies one symbol entry inside the report's file list.
type moveSite struct {
	file int
	pos  int
}

// detectCrossFileMoves pairs a uniquely removed and a uniquely added
// symbol with the same name and kind from different files, reclassifying
// the added entry as moved and dropping its removed counterpart. The
// pairing stays medium confidence: name and kind agreement cannot prove
// the two declarations are the same symbol.
func detectCrossFileMoves(files []DiffFile, notes []string) ([]DiffFile, []string) {
	removed := make(map[string][]moveSite)
	added := make(map[string][]moveSite)
	for i := range files {
		for j, symbol := range files[i].Symbols {
			key := symbol.Name + "#" + symbol.Kind
			switch symbol.Change {
			case DiffChangeRemoved:
				removed[key] = append(removed[key], moveSite{file: i, pos: j})
			case DiffChangeAdded:
				added[key] = append(added[key], moveSite{file: i, pos: j})
			}
		}
	}
	moved := make(map[moveSite]string)
	dropped := make(map[moveSite]bool)
	for key, additions := range added {
		removals := removed[key]
		if len(additions) != 1 || len(removals) != 1 {
			continue
		}
		from, to := removals[0], additions[0]
		if from.file == to.file {
			continue
		}
		moved[to] = files[from.file].Path
		dropped[from] = true
	}
	if len(moved) == 0 {
		return files, notes
	}
	for i := range files {
		kept := files[i].Symbols[:0]
		for j, symbol := range files[i].Symbols {
			site := moveSite{file: i, pos: j}
			if dropped[site] {
				continue
			}
			if from, ok := moved[site]; ok {
				symbol.Change = DiffChangeMoved
				symbol.MovedFrom = from
				symbol.Confidence = ConfidenceMedium
			}
			kept = append(kept, symbol)
		}
		files[i].Symbols = kept
	}
	return files, notes
}

// symbolSpan returns one symbol's half-open line span.
func symbolSpan(symbol analyze.Symbol) lineRange {
	end := symbol.EndLine
	if end < symbol.Location.Line {
		end = symbol.Location.Line
	}
	return lineRange{start: symbol.Location.Line, end: end + 1}
}

// overlapsAny reports whether span intersects any range.
func overlapsAny(span lineRange, ranges []lineRange) bool {
	for _, r := range ranges {
		if span.start < r.end && r.start < span.end {
			return true
		}
	}
	return false
}

// symbolKeyIndex maps name#kind to the before-side symbol for changed-pair
// classification, keeping the first occurrence for determinism.
func symbolKeyIndex(before []analyze.Symbol) map[string]analyze.Symbol {
	index := make(map[string]analyze.Symbol, len(before))
	for _, symbol := range before {
		key := symbol.Name + "#" + symbol.Kind
		if _, exists := index[key]; !exists {
			index[key] = symbol
		}
	}
	return index
}

// sortDiffSymbols orders deterministically: by after line (removed symbols
// last, keeping before-line order), then name.
func sortDiffSymbols(symbols []DiffSymbol) {
	slices.SortFunc(symbols, func(a, b DiffSymbol) int {
		aLine, bLine := a.AfterLine, b.AfterLine
		if aLine == 0 {
			aLine = 1 << 30
		}
		if bLine == 0 {
			bLine = 1 << 30
		}
		if aLine != bLine {
			return aLine - bLine
		}
		if aLine == 1<<30 && aLine == bLine && a.BeforeLine != b.BeforeLine {
			return a.BeforeLine - b.BeforeLine
		}
		return strings.Compare(a.Name, b.Name)
	})
}

// boundedNotes keeps the notes list sorted, deduplicated, and bounded.
func boundedNotes(notes []string) []string {
	if len(notes) == 0 {
		return nil
	}
	out := slices.Clone(notes)
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > diffMaxNotes {
		kept := diffMaxNotes - 1
		extra := len(out) - kept
		out = append(out[:kept], fmt.Sprintf("... (%d more notes)", extra))
	}
	return out
}
