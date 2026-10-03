package gitoutgoing

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/ownedprocess"
	"io"
	"os"
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
	rules := config.Default().OutgoingGit
	ctx, cancel := context.WithTimeout(context.Background(), rules.Timeout)
	defer cancel()
	return rawLogContext(ctx, root, revision, rules)
}
func rawLogContext(ctx context.Context, root, revision string, rules config.OutgoingGitRules) ([]commitDiff, error) {
	cmd := commandFor(ctx, "log", "--reverse", "--no-color", "--no-decorate", "--no-abbrev",
		"--format=%x00%H%x00", "--raw", "-z", "-m", "--root", "--no-renames",
		"--diff-filter=AMT", "--end-of-options", revision)
	cmd.Dir = root
	result, err := ownedprocess.Run(ctx, cmd, ownedprocess.Limits{StdoutBytes: rules.MaxLogBytes, StderrBytes: rules.MaxStderrBytes})
	if err != nil {
		return nil, fmt.Errorf("git log --raw: %w (stderr truncated=%t): %s", err, result.StderrTruncated, strings.TrimSpace(string(result.Stderr)))
	}
	return parseRawLog(result.Stdout)
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
		fields := strings.Fields(string(part))
		if len(fields) != 5 || !isObjectID([]byte(fields[2])) || !isObjectID([]byte(fields[3])) {
			return nil, errors.New("malformed raw diff metadata")
		}
		if index >= len(parts)-1 {
			return nil, fmt.Errorf("diff-tree record without path for %s", commits[len(commits)-1].oid)
		}
		commit := &commits[len(commits)-1]
		commit.records = append(commit.records, rawRecord{meta: part, path: parts[index]})
		index++
	}
	return commits, nil
}

func isObjectID(value []byte) bool {
	if len(value) != 40 && len(value) != 64 {
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
	ctx         context.Context
	cmd         *exec.Cmd
	process     *ownedprocess.Process
	cancel      context.CancelFunc
	stdout      io.ReadCloser
	headerLimit int
	stderr      *retainedStderr
	stdin       io.WriteCloser
	reader      *bufio.Reader
}

// startCatFile launches `git cat-file <mode>` without --buffer so every
// response is flushed immediately.
func startCatFile(root, mode string) (*catFile, error) {
	rules := config.Default().OutgoingGit
	ctx, cancel := context.WithTimeout(context.Background(), rules.Timeout)
	batch, err := startCatFileContext(ctx, root, mode, rules)
	if err != nil {
		cancel()
		return nil, err
	}
	childCancel := batch.cancel
	batch.cancel = func() { childCancel(); cancel() }
	return batch, nil
}
func startCatFileContext(ctx context.Context, root, mode string, rules config.OutgoingGitRules) (*catFile, error) {
	childCtx, cancel := context.WithCancel(ctx)
	cmd := commandFor(childCtx, "cat-file", mode)
	cmd.Dir = root
	stdinR, stdin, err := os.Pipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, stdoutW, err := os.Pipe()
	if err != nil {
		cancel()
		_ = stdinR.Close()
		_ = stdin.Close()
		return nil, err
	}
	stderr := &retainedStderr{limit: rules.MaxStderrBytes}
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = stderr
	process, err := ownedprocess.Start(childCtx, cmd)
	_ = stdinR.Close()
	_ = stdoutW.Close()
	if err != nil {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	// Closing pipe endpoints wakes request/body reads when operation is cancelled.
	go func() { <-childCtx.Done(); _ = stdin.Close(); _ = stdout.Close() }()
	return &catFile{ctx: ctx, cmd: cmd, process: process, cancel: cancel, stdin: stdin, stdout: stdout, reader: bufio.NewReaderSize(stdout, rules.MaxHeaderBytes), headerLimit: rules.MaxHeaderBytes, stderr: stderr}, nil
}

// request writes one object id and reads its response header line.
func (c *catFile) request(oid string) (string, error) {
	if _, err := io.WriteString(c.stdin, oid+"\n"); err != nil {
		return "", errors.Join(err, c.ctx.Err())
	}
	lineBytes, err := c.reader.ReadSlice('\n')
	if len(lineBytes) > c.headerLimit {
		return "", errors.New("cat-file header limit exceeded")
	}
	line := string(lineBytes)
	if err != nil {
		return "", errors.Join(err, c.ctx.Err())
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
		return nil, errors.Join(err, c.ctx.Err())
	}
	if _, err := io.CopyN(io.Discard, c.reader, size-want); err != nil {
		return nil, errors.Join(err, c.ctx.Err())
	}
	trailer := make([]byte, 1)
	if _, err := io.ReadFull(c.reader, trailer); err != nil {
		return nil, errors.Join(err, c.ctx.Err())
	}
	if trailer[0] != '\n' {
		return nil, fmt.Errorf("unexpected cat-file body trailer %#02x", trailer[0])
	}
	return body, nil
}

func (c *catFile) close() error {
	_ = c.stdin.Close()
	err := c.process.Wait()
	c.cancel()
	_ = c.stdout.Close()
	if err != nil {
		return fmt.Errorf("cat-file process: %w (stderr truncated=%t): %s", err, c.stderr.truncated, strings.TrimSpace(string(c.stderr.data)))
	}
	return nil
}

// blobSizes resolves the size of every blob through one --batch-check process,
// replacing one `git cat-file -s` process per blob.
func blobSizes(root string, oids []string) (map[string]int64, error) {
	rules := config.Default().OutgoingGit
	ctx, cancel := context.WithTimeout(context.Background(), rules.Timeout)
	defer cancel()
	return blobSizesContext(ctx, root, oids, rules)
}
func blobSizesContext(ctx context.Context, root string, oids []string, rules config.OutgoingGitRules) (map[string]int64, error) {
	sizes := make(map[string]int64, len(oids))
	if len(oids) == 0 {
		return sizes, nil
	}
	batch, err := startCatFileContext(ctx, root, "--batch-check", rules)
	if err != nil {
		return nil, err
	}
	for _, oid := range oids {
		header, err := batch.request(oid)
		if err != nil {
			batch.cancel()
			_ = batch.close()
			return nil, fmt.Errorf("cat-file --batch-check %s: %w", oid, err)
		}
		size, ok := blobSizeFromHeader(oid, header)
		if !ok {
			batch.cancel()
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
	rules := config.Default().OutgoingGit
	ctx, cancel := context.WithTimeout(context.Background(), rules.Timeout)
	defer cancel()
	return blobFormatsContext(ctx, root, oids, rules)
}
func blobFormatsContext(ctx context.Context, root string, oids []string, rules config.OutgoingGitRules) (map[string]string, error) {
	return blobFormatsExpectedContext(ctx, root, oids, rules, nil)
}
func blobFormatsExpectedContext(ctx context.Context, root string, oids []string, rules config.OutgoingGitRules, expected map[string]int64) (map[string]string, error) {
	formats := make(map[string]string, len(oids))
	if len(oids) == 0 {
		return formats, nil
	}
	batch, err := startCatFileContext(ctx, root, "--batch", rules)
	if err != nil {
		return nil, err
	}
	for _, oid := range oids {
		header, err := batch.request(oid)
		if err != nil {
			batch.cancel()
			_ = batch.close()
			return nil, fmt.Errorf("cat-file --batch %s: %w", oid, err)
		}
		size, ok := blobSizeFromHeader(oid, header)
		if !ok {
			batch.cancel()
			_ = batch.close()
			return nil, fmt.Errorf("object %s is not an available blob: %q", oid, header)
		}
		if expected != nil && expected[oid] != size {
			batch.cancel()
			_ = batch.close()
			return nil, fmt.Errorf("cat-file blob size changed for %s", oid)
		}
		sample, err := batch.readBody(size, sampleSize)
		if err != nil {
			batch.cancel()
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
