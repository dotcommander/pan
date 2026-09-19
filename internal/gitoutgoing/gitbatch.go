package gitoutgoing

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// commitDiff pairs one commit with its ordered --raw change records.
type commitDiff struct {
	oid     string
	records []rawRecord
}

// rawRecord is one `--raw -z` change entry: metadata then the unquoted path.
type rawRecord struct {
	meta []byte
	path []byte
}

// rawLog streams one `git log --raw -z` query and returns commit records in
// oldest-first order. It replaces one diff-tree process per commit with a
// single repository query.
func rawLog(root, revision string) ([]commitDiff, error) {
	cmd := exec.Command("git", "log", "--reverse", "--no-color", "--no-decorate", "--no-abbrev",
		"--format=%x00%H%x00", "--raw", "-z", "-m", "--root", "--no-renames",
		"--diff-filter=AMT", "--end-of-options", revision)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git log --raw: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseRawLog(stdout.Bytes())
}

// parseRawLog splits the NUL-delimited log stream into commits and their
// records. A parser state distinguishes commit ids from paths, so every byte
// Git permits in a path remains ordinary path content.
func parseRawLog(output []byte) ([]commitDiff, error) {
	var commits []commitDiff
	parts := bytes.Split(output, []byte{0})
	for index := 0; index < len(parts); {
		part := bytes.Trim(parts[index], "\n")
		index++
		if len(part) == 0 {
			continue
		}
		if isObjectID(part) {
			commits = append(commits, commitDiff{oid: string(part)})
			continue
		}
		if len(commits) == 0 {
			return nil, errors.New("raw record before first commit id")
		}
		if part[0] != ':' {
			return nil, fmt.Errorf("unexpected diff-tree record for %s", commits[len(commits)-1].oid)
		}
		if index >= len(parts) {
			return nil, fmt.Errorf("diff-tree record without path for %s", commits[len(commits)-1].oid)
		}
		commit := &commits[len(commits)-1]
		commit.records = append(commit.records, rawRecord{meta: part, path: parts[index]})
		index++
	}
	return commits, nil
}

func isObjectID(value []byte) bool {
	if len(value) != 40 {
		return false
	}
	for _, char := range value {
		if !('0' <= char && char <= '9') && !('a' <= char && char <= 'f') {
			return false
		}
	}
	return true
}

// catFile is one long-lived `git cat-file` process answering sequential
// object requests over stdin/stdout pipes.
type catFile struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
}

// startCatFile launches `git cat-file <mode>` without --buffer so every
// response is flushed immediately.
func startCatFile(root, mode string) (*catFile, error) {
	cmd := exec.Command("git", "cat-file", mode)
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &catFile{cmd: cmd, stdin: stdin, reader: bufio.NewReaderSize(stdout, 1<<16)}, nil
}

// request writes one object id and reads its response header line.
func (c *catFile) request(oid string) (string, error) {
	if _, err := io.WriteString(c.stdin, oid+"\n"); err != nil {
		return "", err
	}
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\n"), nil
}

// readBody consumes a --batch response body of size bytes plus the trailing
// newline, retaining at most limit bytes.
func (c *catFile) readBody(size int64, limit int) ([]byte, error) {
	want := size
	if int64(limit) < want {
		want = int64(limit)
	}
	body := make([]byte, want)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return nil, err
	}
	if _, err := io.CopyN(io.Discard, c.reader, size-want); err != nil {
		return nil, err
	}
	trailer := make([]byte, 1)
	if _, err := io.ReadFull(c.reader, trailer); err != nil {
		return nil, err
	}
	if trailer[0] != '\n' {
		return nil, fmt.Errorf("unexpected cat-file body trailer %#02x", trailer[0])
	}
	return body, nil
}

func (c *catFile) close() error {
	_ = c.stdin.Close()
	return c.cmd.Wait()
}

// blobSizes resolves the size of every blob through one --batch-check process,
// replacing one `git cat-file -s` process per blob.
func blobSizes(root string, oids []string) (map[string]int64, error) {
	sizes := make(map[string]int64, len(oids))
	if len(oids) == 0 {
		return sizes, nil
	}
	batch, err := startCatFile(root, "--batch-check")
	if err != nil {
		return nil, err
	}
	for _, oid := range oids {
		header, err := batch.request(oid)
		if err != nil {
			_ = batch.close()
			return nil, fmt.Errorf("cat-file --batch-check %s: %w", oid, err)
		}
		size, ok := blobSizeFromHeader(oid, header)
		if !ok {
			_ = batch.close()
			return nil, fmt.Errorf("object %s is not an available blob: %q", oid, header)
		}
		sizes[oid] = size
	}
	if err := batch.close(); err != nil {
		return nil, fmt.Errorf("cat-file --batch-check: %w", err)
	}
	return sizes, nil
}

func blobSizeFromHeader(oid, header string) (int64, bool) {
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[0] != oid || fields[1] != "blob" {
		return 0, false
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return 0, false
	}
	return size, true
}

// blobFormats samples each blob through one --batch process and returns the
// executable format for blobs whose leading bytes identify one, replacing one
// probe plus one read process per blob.
func blobFormats(root string, oids []string) (map[string]string, error) {
	formats := make(map[string]string, len(oids))
	if len(oids) == 0 {
		return formats, nil
	}
	batch, err := startCatFile(root, "--batch")
	if err != nil {
		return nil, err
	}
	for _, oid := range oids {
		header, err := batch.request(oid)
		if err != nil {
			_ = batch.close()
			return nil, fmt.Errorf("cat-file --batch %s: %w", oid, err)
		}
		size, ok := blobSizeFromHeader(oid, header)
		if !ok {
			_ = batch.close()
			return nil, fmt.Errorf("object %s is not an available blob: %q", oid, header)
		}
		sample, err := batch.readBody(size, sampleSize)
		if err != nil {
			_ = batch.close()
			return nil, fmt.Errorf("cat-file --batch %s: %w", oid, err)
		}
		if format := executableFormat(sample); format != "" {
			formats[oid] = format
		}
	}
	if err := batch.close(); err != nil {
		return nil, fmt.Errorf("cat-file --batch: %w", err)
	}
	return formats, nil
}
