// check-git-push-content examines blobs added by an outgoing Git revision.
package gitoutgoing

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
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

func scan(root, revision string, maxBlobBytes int64) (Report, error) {
	report := Report{Revision: revision, MaxBlobBytes: maxBlobBytes, Paths: []PathEvidence{}, Findings: []Finding{}}
	commits, err := gitLines(root, "rev-list", "--reverse", "--end-of-options", revision)
	if err != nil {
		return report, err
	}
	seen := make(map[string]bool)
	blobs := make(map[string]blobInfo)
	findings := make([]Finding, 0)
	for _, commit := range commits {
		changes, err := gitNUL(root, "diff-tree", "--no-renames", "--no-commit-id", "--root", "-r", "-m", "--diff-filter=AMT", "--raw", "-z", commit)
		if err != nil {
			return report, err
		}
		for i := 0; i+1 < len(changes); i += 2 {
			header, path := string(changes[i]), string(changes[i+1])
			fields := strings.Fields(header)
			if len(fields) < 5 || !strings.HasPrefix(header, ":") {
				return report, fmt.Errorf("unexpected diff-tree record for %s", commit)
			}
			if len(report.Paths) < maxPathEvidence {
				report.Paths = append(report.Paths, PathEvidence{Commit: commit, Path: path})
			} else {
				report.PathsTruncated = true
			}
			for _, finding := range classifyPath(commit, path) {
				addFinding(&findings, seen, finding)
			}
			if !regularFileMode(fields[1]) {
				continue
			}
			oid := fields[3]
			blob, ok := blobs[oid]
			if !ok {
				blob.size, err = blobSize(root, oid)
				if err != nil {
					return report, err
				}
				blobs[oid] = blob
			}
			size := blob.size
			if size > maxBlobBytes {
				addFinding(&findings, seen, Finding{
					Level: "blocked", Rule: "large-blob", Commit: commit, Path: path, Size: size,
				})
				continue
			}
			if !blob.sampled {
				sample, err := blobSample(root, oid)
				if err != nil {
					return report, err
				}
				blob.format = executableFormat(sample)
				blob.sampled = true
				blobs[oid] = blob
			}
			if blob.format != "" {
				addFinding(&findings, seen, Finding{
					Level: "blocked", Rule: "executable-content", Commit: commit, Path: path,
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

type blobInfo struct {
	size    int64
	format  string
	sampled bool
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

func blobSample(root, oid string) ([]byte, error) {
	probe := exec.Command("git", "cat-file", "-e", oid+"^{blob}")
	probe.Dir = root
	if err := probe.Run(); err != nil {
		return nil, err
	}
	cmd := exec.Command("git", "cat-file", "blob", oid)
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	sample, readErr := io.ReadAll(io.LimitReader(stdout, sampleSize))
	_ = stdout.Close()
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil && len(sample) < sampleSize {
		return nil, waitErr
	}
	return sample, nil
}

func blobSize(root, oid string) (int64, error) {
	cmd := exec.Command("git", "cat-file", "-s", oid)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil || size < 0 {
		return 0, fmt.Errorf("invalid blob size for %s", oid)
	}
	return size, nil
}

func gitLines(root string, args ...string) ([]string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(output)), nil
}

func gitNUL(root string, args ...string) ([][]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	records := bytes.Split(output, []byte{0})
	if len(records) > 0 && len(records[len(records)-1]) == 0 {
		records = records[:len(records)-1]
	}
	if len(records)%2 != 0 {
		return nil, errors.New("odd number of diff-tree records")
	}
	return records, nil
}

func quoteGitPath(path string) string {
	for _, r := range path {
		if r < 0x20 || r == 0x7f || r == '\\' || r == '"' {
			return strconv.Quote(path)
		}
	}
	return path
}
