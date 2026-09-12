package clean

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// maxGitOutputBytes bounds every git plumbing read, mirroring the scan
// package's hygiene bound.
const maxGitOutputBytes = 4 << 20

// gitInfo is the read-only repository view used to enrich walked files.
// When available is false the repository state could not be read and every
// cleanup decision is suppressed. When insideRepo is false the root is not
// a work tree, which is not an error: every file is simply untracked.
type gitInfo struct {
	available  bool
	insideRepo bool
	prefix     string // repo-root-relative prefix of the scan root, slash form
	tracked    map[string]bool
	ignored    map[string]bool
	staleDays  map[string]int
	deleted    map[string]bool
	cause      string
}

// loadGitInfo probes the work tree and reads tracked/ignored path sets plus
// a bounded history window. Every git invocation is a direct argv plumbing
// command with bounded output; no shell is involved.
func loadGitInfo(ctx context.Context, root string, opts Options) gitInfo {
	info := gitInfo{available: true, tracked: map[string]bool{}, ignored: map[string]bool{}, staleDays: map[string]int{}, deleted: map[string]bool{}}

	inside, err := gitRun(ctx, root, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if isNotARepository(err) {
			return info // plain directory: everything untracked
		}
		info.available = false
		info.cause = err.Error()
		return info
	}
	if strings.TrimSpace(inside) != "true" {
		return info // inside .git itself: treat as plain directory
	}
	info.insideRepo = true

	prefixOut, err := gitRun(ctx, root, "rev-parse", "--show-prefix")
	if err != nil {
		info.available = false
		info.cause = err.Error()
		return info
	}
	info.prefix = strings.TrimSpace(prefixOut)

	trackedOut, err := gitRun(ctx, root, "ls-files", "--cached", "-z")
	if err != nil {
		info.available = false
		info.cause = err.Error()
		return info
	}
	for _, p := range splitNUL(trackedOut) {
		info.tracked[p] = true
	}

	ignoredOut, err := gitRun(ctx, root, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		info.available = false
		info.cause = err.Error()
		return info
	}
	for _, p := range splitNUL(ignoredOut) {
		info.ignored[p] = true
	}

	return info
}

// loadHistory reads the latest all-ref commit for each tracked path, then
// records deletions from a bounded all-ref history window. Staleness must not
// depend on unrelated recent commits, while orphan evidence is intentionally
// bounded by MaxHistoryCommits. History read failures degrade to absent history
// rather than unknown repository state: the index sets were already read.
func (g *gitInfo) loadHistory(ctx context.Context, root string, opts Options, files []FileInfo) {
	g.loadStaleness(ctx, root, opts, files)

	out, err := gitRun(ctx, root, "log", "--all", "--no-merges", "-n", strconv.Itoa(opts.Rules.MaxHistoryCommits),
		"--name-status", "--format=%x01%cI")
	if err != nil {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "\t"):
			g.recordDeletion(line)
		}
	}
}

// loadStaleness finds each tracked path's latest all-ref change. One bounded
// command per path avoids losing an old path solely because a repository has
// more history than the deletion evidence window.
func (g *gitInfo) loadStaleness(ctx context.Context, root string, opts Options, files []FileInfo) {
	now := opts.now()
	seen := make(map[string]bool)
	for _, file := range files {
		if file.IsDir {
			continue
		}
		path := g.repoRelative(file.RelPath)
		if !g.tracked[path] || seen[path] {
			continue
		}
		seen[path] = true
		out, err := gitRun(ctx, root, "log", "--all", "--no-merges", "-1", "--format=%cI", "--", path)
		if err != nil {
			continue
		}
		when, err := time.Parse(time.RFC3339, strings.TrimSpace(out))
		if err != nil {
			continue
		}
		g.staleDays[path] = int(now.Sub(when).Hours() / 24)
	}
}

func (g *gitInfo) recordDeletion(line string) {
	fields := strings.Split(line, "\t")
	if len(fields) < 2 {
		return
	}
	status := fields[0]
	paths := fields[1:]
	// Rename and copy lines carry the old and new path; both count as
	// touched, so only a plain deletion records a deleted path here.
	isRenameOrCopy := strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C")
	if !isRenameOrCopy && status == "D" && len(paths) > 0 {
		g.deleted[paths[0]] = true
	}
}

// repoRelative maps a scan-root-relative path to its repository-root key.
func (g gitInfo) repoRelative(rel string) string {
	return g.prefix + filepath.ToSlash(rel)
}

// mark applies repository state onto walked files. Tracked and ignored come
// from the index and exclude views; staleness applies to tracked files at or
// beyond the configured threshold; orphaned flags untracked paths deleted in
// the bounded history window but still present on disk.
func (g gitInfo) mark(files []FileInfo, opts Options) {
	threshold := opts.staleDays()
	for i := range files {
		if files[i].IsDir {
			continue
		}
		if !g.available {
			files[i].GitStateUnknown = true
			continue
		}
		key := g.repoRelative(files[i].RelPath)
		files[i].Tracked = g.tracked[key]
		if files[i].Tracked {
			if days, found := g.staleDays[key]; found && days >= threshold {
				files[i].StaleDays = days
			}
		} else if g.deleted[key] {
			files[i].Orphaned = true
		}
		if !files[i].Tracked {
			files[i].Ignored = g.ignored[key]
		}
	}
}

// gitRun executes one git plumbing command in dir with bounded output.
// stdout is capped at maxGitOutputBytes; stderr is capped at 4 KiB.
func gitRun(ctx context.Context, dir string, args ...string) (string, error) {
	// The command name is a constant and every argument arrives through
	// cmd.Args: git runs as one direct argv process in dir, never through
	// a shell.
	cmd := exec.CommandContext(ctx, cmdGit)
	cmd.Dir = dir
	cmd.Args = append(cmd.Args, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("git stdout: %w", err)
	}
	stderr := &limitBuffer{max: 4 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start git: %w", err)
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, maxGitOutputBytes+1))
	waitErr := cmd.Wait()
	if readErr != nil {
		return "", fmt.Errorf("read git output: %w", readErr)
	}
	if len(out) > maxGitOutputBytes {
		return "", fmt.Errorf("git %s output exceeded %d bytes", args[0], maxGitOutputBytes)
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			return "", fmt.Errorf("git %s: %w", args[0], waitErr)
		}
		return "", fmt.Errorf("git %s: %w: %s", args[0], waitErr, reason)
	}
	return string(out), nil
}

// limitBuffer keeps at most max bytes, discarding the rest.
type limitBuffer struct {
	buf bytes.Buffer
	max int
}

func (l *limitBuffer) Write(p []byte) (int, error) {
	room := l.max - l.buf.Len()
	if room > 0 {
		if len(p) <= room {
			l.buf.Write(p)
		} else {
			l.buf.Write(p[:room])
		}
	}
	return len(p), nil
}

func (l *limitBuffer) String() string { return l.buf.String() }

func splitNUL(out string) []string {
	if out == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(out, "\x00"), "\x00")
}

func isNotARepository(err error) bool {
	return strings.Contains(err.Error(), "not a git repository")
}
