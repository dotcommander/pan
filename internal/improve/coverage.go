package improve

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// CensusSchema stamps every coverage census report.
const CensusSchema = "pan.improve-census/v1"

// Block is one statement range from a Go coverprofile.
type Block struct {
	File       string // slash, repo-relative
	StartLine  int
	EndLine    int
	Statements int
	Count      int
}

// Gap is one uncovered statement range inside a source file.
type Gap struct {
	StartLine  int `json:"start_line"`
	EndLine    int `json:"end_line"`
	Statements int `json:"statements"`
}

// FileCoverage aggregates coverprofile blocks for one non-test source
// file, in pan's deterministic, sorted output shape.
type FileCoverage struct {
	File                string  `json:"file"`
	TotalStatements     int     `json:"total_statements"`
	CoveredStatements   int     `json:"covered_statements"`
	UncoveredStatements int     `json:"uncovered_statements"`
	Coverage            float64 `json:"coverage_percent"`
	Gaps                []Gap   `json:"gaps,omitempty"`
}

// PackageCensus is the deterministic coverage census for one package
// directory, mirroring Pan improvement's census fields.
type PackageCensus struct {
	Package             string  `json:"package"`
	TotalStatements     int     `json:"total_statements"`
	CoveredStatements   int     `json:"covered_statements"`
	ReachableUncovered  int     `json:"reachable_uncovered"`
	Coverage            float64 `json:"coverage_percent"`
	TractabilityDensity float64 `json:"tractability_density"`
}

// CensusReport is the standalone census output consumed by planning and
// by `improve prep`/`improve recommend`.
type CensusReport struct {
	Schema            string          `json:"schema"`
	Mode              string          `json:"mode"`
	Packages          []PackageCensus `json:"packages"`
	Files             int             `json:"files"`
	TotalStatements   int             `json:"total_statements"`
	CoveredStatements int             `json:"covered_statements"`
	TotalCoverage     float64         `json:"total_coverage_percent"`
	CoverageMeasured  bool            `json:"coverage_measured"`
}

// ParseCoverProfile parses a Go coverprofile into blocks. File names are
// trimmed of a module prefix when go.mod names one, so block files are
// repo-relative slash paths. Test files are excluded: the census and
// worklist describe production statements.
func ParseCoverProfile(path, modulePath string) ([]Block, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("open coverprofile: %w", err)
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	mode := ""
	prefix := ""
	if modulePath != "" {
		prefix = modulePath + "/"
	}
	lineNo := 0
	var blocks []Block
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if lineNo == 1 {
			if !strings.HasPrefix(line, "mode:") {
				return nil, "", errors.New("coverprofile line 1: missing mode header")
			}
			mode = strings.TrimSpace(strings.TrimPrefix(line, "mode:"))
			continue
		}
		block, err := parseBlockLine(line, prefix)
		if err != nil {
			return nil, "", fmt.Errorf("coverprofile line %d: %w", lineNo, err)
		}
		if block != nil {
			blocks = append(blocks, *block)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, "", fmt.Errorf("read coverprofile: %w", err)
	}
	return blocks, mode, nil
}

// parseBlockLine parses "file.go:start.l,end.l numStmts count". It
// returns nil for test files.
func parseBlockLine(line, modulePrefix string) (*Block, error) {
	location, countPart, ok := strings.Cut(line, " ")
	if !ok {
		return nil, fmt.Errorf("malformed block %q", line)
	}
	fields := strings.Fields(countPart)
	if len(fields) != 2 {
		return nil, fmt.Errorf("malformed counts %q", countPart)
	}
	statements, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil, fmt.Errorf("statement count: %w", err)
	}
	count, err := strconv.Atoi(fields[1])
	if err != nil {
		return nil, fmt.Errorf("hit count: %w", err)
	}
	name, pos, ok := strings.Cut(location, ":")
	if !ok {
		return nil, fmt.Errorf("malformed location %q", location)
	}
	if strings.HasSuffix(name, "_test.go") {
		return nil, nil
	}
	name = strings.TrimPrefix(filepath.ToSlash(name), modulePrefix)
	startPart, endPart, ok := strings.Cut(pos, ",")
	if !ok {
		return nil, fmt.Errorf("malformed range %q", pos)
	}
	startLine, err := parsePos(startPart)
	if err != nil {
		return nil, err
	}
	endLine, err := parsePos(endPart)
	if err != nil {
		return nil, err
	}
	return &Block{File: name, StartLine: startLine, EndLine: endLine, Statements: statements, Count: count}, nil
}

// parsePos validates one "line.col" coverprofile position and returns its
// line. The column is validated but not returned: callers only consume the
// line.
func parsePos(part string) (int, error) {
	l, c, ok := strings.Cut(part, ".")
	if !ok {
		return 0, fmt.Errorf("malformed position %q", part)
	}
	line, err := strconv.Atoi(l)
	if err != nil {
		return 0, fmt.Errorf("position line: %w", err)
	}
	if _, err := strconv.Atoi(c); err != nil {
		return 0, fmt.Errorf("position column: %w", err)
	}
	return line, nil
}

// BuildCensus folds blocks into the deterministic per-package census with
// repo totals. Percents are rounded to two decimals to keep output stable.
func BuildCensus(blocks []Block, mode string) CensusReport {
	report := CensusReport{Schema: CensusSchema, Mode: mode, CoverageMeasured: true}
	if blocks == nil {
		report.Packages = []PackageCensus{}
		return report
	}
	byPackage := make(map[string]*PackageCensus)
	fileIndex := make(map[string]int)
	var files []string
	for _, block := range blocks {
		pkg := filepath.ToSlash(filepath.Dir(block.File))
		if pkg == "." {
			pkg = "."
		}
		item := byPackage[pkg]
		if item == nil {
			item = &PackageCensus{Package: pkg}
			byPackage[pkg] = item
		}
		item.TotalStatements += block.Statements
		report.TotalStatements += block.Statements
		if block.Count > 0 {
			item.CoveredStatements += block.Statements
			report.CoveredStatements += block.Statements
		} else {
			item.ReachableUncovered += block.Statements
		}
		if _, seen := fileIndex[block.File]; !seen {
			fileIndex[block.File] = len(files)
			files = append(files, block.File)
		}
	}
	sort.Strings(files)
	report.Files = len(files)
	packages := make([]PackageCensus, 0, len(byPackage))
	for _, item := range byPackage {
		if item.TotalStatements > 0 {
			item.Coverage = percent(item.CoveredStatements, item.TotalStatements)
			item.TractabilityDensity = round2(float64(item.ReachableUncovered) / float64(item.TotalStatements))
		}
		packages = append(packages, *item)
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].Package < packages[j].Package })
	report.Packages = packages
	report.TotalCoverage = percent(report.CoveredStatements, report.TotalStatements)
	return report
}

// FileCoverages folds blocks into per-file coverage with uncovered gaps,
// sorted by file path.
func FileCoverages(blocks []Block) []FileCoverage {
	index := make(map[string]*FileCoverage)
	var files []string
	for _, block := range blocks {
		item := index[block.File]
		if item == nil {
			item = &FileCoverage{File: block.File}
			index[block.File] = item
			files = append(files, block.File)
		}
		item.TotalStatements += block.Statements
		if block.Count > 0 {
			item.CoveredStatements += block.Statements
		} else if block.Statements > 0 {
			item.UncoveredStatements += block.Statements
			item.Gaps = append(item.Gaps, Gap{StartLine: block.StartLine, EndLine: block.EndLine, Statements: block.Statements})
		}
	}
	sort.Strings(files)
	out := make([]FileCoverage, 0, len(files))
	for _, name := range files {
		item := index[name]
		item.Coverage = percent(item.CoveredStatements, item.TotalStatements)
		out = append(out, *item)
	}
	return out
}

func percent(covered, total int) float64 {
	if total <= 0 {
		return 0
	}
	return round2(100 * float64(covered) / float64(total))
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// ReadModulePath extracts the module path from <root>/go.mod, or "" when
// unreadable. Profile file names are module-qualified; the module path
// trims them to repo-relative form.
func ReadModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if module, ok := strings.CutPrefix(trimmed, "module "); ok {
			return strings.TrimSpace(strings.Trim(strings.TrimSpace(module), `"`))
		}
	}
	return ""
}
