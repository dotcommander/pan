// check-git-push-content examines blobs added by an outgoing Git revision.
package gitoutgoing

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/dotcommander/pan/internal/config"
	"sort"
	"strconv"
	"strings"
)

const sampleSize = 65536
const maxPathEvidence = 10000

type Finding struct {
	Level  string `json:"level"`
	Rule   string `json:"rule"`
	Commit string `json:"commit"`
	Path   string `json:"path"`
	Size   int64  `json:"size_bytes,omitempty"`
}

type PathEvidence struct {
	Commit string `json:"commit"`
	Path   string `json:"path"`
}

type Report struct {
	Revision       string         `json:"revision"`
	MaxBlobBytes   int64          `json:"max_blob_bytes"`
	Paths          []PathEvidence `json:"paths"`
	PathsTruncated bool           `json:"paths_truncated"`
	Findings       []Finding      `json:"findings"`
	Blocked        bool           `json:"blocked"`
}

// scan examines every change record in the revision through one git log
// stream, one --batch-check size pass, and one --batch sample pass.
func scan(root, revision string, maxBlobBytes int64) (Report, error) {
	return Inspect(revision, root, maxBlobBytes)
}
func scanContext(ctx context.Context, root, revision string, maxBlobBytes int64, rules config.OutgoingGitRules) (Report, error) {
	report := Report{Revision: revision, MaxBlobBytes: maxBlobBytes, Paths: []PathEvidence{}, Findings: []Finding{}}
	commits, err := rawLogContext(ctx, root, revision, rules)
	if err != nil {
		return report, err
	}

	// Collect unique regular-file blob ids first-seen so size and sample
	// lookups each run through a single long-lived cat-file process.
	blobOrder := make([]string, 0)
	isRegularBlob := make(map[string]bool)
	for _, commit := range commits {
		for _, record := range commit.records {
			fields := strings.Fields(string(record.meta))
			if len(fields) < 5 {
				return report, fmt.Errorf("unexpected diff-tree record for %s", commit.oid)
			}
			if !regularFileMode(fields[1]) {
				continue
			}
			oid := fields[3]
			if !isRegularBlob[oid] {
				isRegularBlob[oid] = true
				blobOrder = append(blobOrder, oid)
			}
		}
	}

	sizes, err := blobSizesContext(ctx, root, blobOrder, rules)
	if err != nil {
		return report, err
	}
	sampleOrder := make([]string, 0, len(blobOrder))
	for _, oid := range blobOrder {
		if sizes[oid] <= maxBlobBytes {
			sampleOrder = append(sampleOrder, oid)
		}
	}
	formats, err := blobFormatsExpectedContext(ctx, root, sampleOrder, rules, sizes)
	if err != nil {
		return report, err
	}

	seen := make(map[string]bool)
	findings := make([]Finding, 0)
	for _, commit := range commits {
		for _, record := range commit.records {
			path := string(record.path)
			fields := strings.Fields(string(record.meta))
			if len(fields) < 5 {
				return report, fmt.Errorf("unexpected diff-tree record for %s", commit.oid)
			}
			if len(report.Paths) < maxPathEvidence {
				report.Paths = append(report.Paths, PathEvidence{Commit: commit.oid, Path: path})
			} else {
				report.PathsTruncated = true
			}
			for _, finding := range classifyPath(commit.oid, path) {
				addFinding(&findings, seen, finding)
			}
			if !regularFileMode(fields[1]) {
				continue
			}
			oid := fields[3]
			size, ok := sizes[oid]
			if !ok {
				return report, fmt.Errorf("missing blob size for %s", oid)
			}
			if size > maxBlobBytes {
				addFinding(&findings, seen, Finding{
					Level: "blocked", Rule: "large-blob", Commit: commit.oid, Path: path, Size: size,
				})
				continue
			}
			if blobFormat := formats[oid]; blobFormat != "" {
				addFinding(&findings, seen, Finding{
					Level: "blocked", Rule: "executable-content", Commit: commit.oid, Path: path,
				})
			}
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		return a.Level+a.Rule+a.Commit+a.Path < b.Level+b.Rule+b.Commit+b.Path
	})
	report.Findings = findings
	for _, finding := range findings {
		if finding.Level == "blocked" {
			report.Blocked = true
			break
		}
	}
	return report, nil
}

func regularFileMode(mode string) bool {
	return len(mode) == 6 && strings.HasPrefix(mode, "100")
}

func addFinding(findings *[]Finding, seen map[string]bool, finding Finding) {
	key := finding.Level + "\x00" + finding.Rule + "\x00" + finding.Commit + "\x00" + finding.Path
	if !seen[key] {
		seen[key] = true
		*findings = append(*findings, finding)
	}
}

func classifyPath(commit, path string) []Finding {
	var findings []Finding
	addBlockedPath := func(rule string) {
		findings = append(findings, Finding{Level: "blocked", Rule: rule, Commit: commit, Path: path})
	}
	if pathComponent(path, ".work") {
		addBlockedPath("private-work-directory")
	}
	if pathComponent(path, ".claude") {
		addBlockedPath("private-claude-directory")
	}
	if environmentPath(path) {
		addBlockedPath("environment-file")
	}
	if basename(path) == "auth-state.json" || strings.HasSuffix(basename(path), "-auth-state.json") {
		addBlockedPath("browser-auth-state")
	}
	if name := basename(path); name == "cookies.txt" || name == "cookies.json" || strings.HasSuffix(name, ".cookies.txt") {
		addBlockedPath("browser-cookie-file")
	}
	if pathComponent(path, ".agent-browser-state") {
		addBlockedPath("browser-agent-state-directory")
	}

	return findings
}

func basename(path string) string {
	if index := strings.LastIndexByte(path, '/'); index >= 0 {
		return path[index+1:]
	}
	return path
}

func pathComponent(path, component string) bool {
	for _, part := range strings.Split(path, "/") {
		if part == component {
			return true
		}
	}
	return false
}

func environmentPath(path string) bool {
	name := basename(path)
	if name == ".env" {
		return true
	}
	if !strings.HasPrefix(name, ".env.") {
		return false
	}
	return name != ".env.example" && name != ".env.sample" && name != ".env.template"
}

func executableFormat(sample []byte) string {
	if len(sample) >= 4 && bytes.Equal(sample[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return "ELF"
	}
	if len(sample) >= 4 {
		switch string(sample[:4]) {
		case "\xfe\xed\xfa\xce", "\xfe\xed\xfa\xcf", "\xce\xfa\xed\xfe", "\xcf\xfa\xed\xfe":
			return "Mach-O"
		case "\xca\xfe\xba\xbe", "\xca\xfe\xba\xbf", "\xbe\xba\xfe\xca", "\xbf\xba\xfe\xca":
			if isMachOFat(sample) {
				return "Mach-O"
			}
		}
	}
	if len(sample) >= 64 && bytes.Equal(sample[:2], []byte{'M', 'Z'}) {
		offset := binary.LittleEndian.Uint32(sample[0x3c:0x40])
		if offset <= uint32(len(sample)-4) && bytes.Equal(sample[offset:offset+4], []byte{'P', 'E', 0, 0}) {
			return "PE"
		}
	}
	return ""
}

func isMachOFat(sample []byte) bool {
	if len(sample) < 8 {
		return false
	}
	var order binary.ByteOrder = binary.BigEndian
	if bytes.Equal(sample[:4], []byte{0xbe, 0xba, 0xfe, 0xca}) || bytes.Equal(sample[:4], []byte{0xbf, 0xba, 0xfe, 0xca}) {
		order = binary.LittleEndian
	}
	narch := order.Uint32(sample[4:8])
	if narch == 0 || narch > 20 {
		return false
	}
	entrySize := uint32(20)
	if bytes.Equal(sample[:4], []byte{0xca, 0xfe, 0xba, 0xbf}) || bytes.Equal(sample[:4], []byte{0xbf, 0xba, 0xfe, 0xca}) {
		entrySize = 32
	}
	return uint64(8)+uint64(narch)*uint64(entrySize) <= uint64(len(sample))
}

func quoteGitPath(path string) string {
	for _, r := range path {
		if r < 0x20 || r == 0x7f || r == '\\' || r == '"' {
			return strconv.Quote(path)
		}
	}
	return path
}
