// Package cache persists bounded analysis snapshots keyed by repository
// root and validates their freshness from file identity stamps and SHA-256
// content manifests. The store lives outside the target repository (the user
// cache directory by default) so analysis commands stay read-only; only the
// explicit cache lifecycle commands write it. Quick inspection checks file
// metadata stamps (size and mtime) across the eligible tree, while full
// validation verifies every captured content hash in the manifest and guards
// against mutations during capture.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
)

// Version is the on-disk cache format version.
const Version = 2

// EntryFile is the single file this package owns inside a cache directory.
const EntryFile = "analysis.json"

// Entry is the persisted cache record.
type Entry struct {
	Version           int                          `json:"version"`
	SchemaVersion     string                       `json:"schema_version"`
	AnalyzerRevision  string                       `json:"analyzer_revision"`
	Root              string                       `json:"root"`
	BuiltAt           time.Time                    `json:"built_at"`
	ConfigFingerprint string                       `json:"config_fingerprint"`
	Snapshot          analyze.Snapshot             `json:"snapshot"`
	Stamps            map[string]analyze.FileStamp `json:"stamps"`
	// Manifest contains content hashes. Raw source is deliberately not persisted.
	Manifest map[string]string `json:"manifest"`
}

// Status describes the usability and freshness of one cache entry.
type Status struct {
	CachePath    string     `json:"cache_path"`
	Exists       bool       `json:"exists"`
	Usable       bool       `json:"usable"`
	Stale        bool       `json:"stale"`
	Reason       string     `json:"reason,omitempty"`
	Root         string     `json:"root,omitempty"`
	BuiltAt      *time.Time `json:"built_at,omitempty"`
	TrackedFiles int        `json:"tracked_files,omitempty"`
	Version      int        `json:"version,omitempty"`
}

// ClearResult reports what one clear operation removed.
type ClearResult struct {
	CachePath string   `json:"cache_path"`
	Removed   []string `json:"removed"`
	Reason    string   `json:"reason,omitempty"`
}

// DefaultDir returns the default cache root: the user cache directory plus
// "pan".
func DefaultDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory: %w", err)
	}
	return filepath.Join(base, "pan"), nil
}

// DirFor derives one cache directory for root under base. Root paths map to
// stable directory names so multiple checkouts can coexist.
func DirFor(base, root string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return filepath.Join(base, hex.EncodeToString(sum[:])[:16])
}

// Fingerprint canonicalizes the analysis bounds a snapshot was built under.
// A cache entry produced under different bounds is a different analysis and
// must not be reused as-is.
func Fingerprint(cfg config.Config) string {
	normalized := cfg.Normalized()
	data, err := json.Marshal(struct {
		MaxFiles      int      `json:"max_files"`
		MaxFileBytes  int64    `json:"max_file_bytes"`
		MaxTotalBytes int64    `json:"max_total_bytes"`
		MaxNodes      int      `json:"max_nodes"`
		Exclude       []string `json:"exclude"`
	}{
		MaxFiles:      normalized.MaxFiles,
		MaxFileBytes:  normalized.MaxFileBytes,
		MaxTotalBytes: normalized.MaxTotalBytes,
		MaxNodes:      normalized.MaxNodes,
		Exclude:       normalized.Exclude,
	})
	if err != nil {
		return ""
	}
	return string(data)
}

// SnapshotID returns the canonical identity of a compiled snapshot generation.
// Request-local source/freshness metadata and diagnostics do not affect identity.
func SnapshotID(cfg config.Config, snap analyze.Snapshot, manifest map[string]string) string {
	identity := snap
	identity.Status.Snapshot = nil
	identity.Diagnostics = nil
	data, _ := json.Marshal(struct {
		Schema           string            `json:"schema"`
		AnalyzerRevision string            `json:"analyzer_revision"`
		Config           string            `json:"config"`
		Manifest         map[string]string `json:"manifest"`
		Snapshot         analyze.Snapshot  `json:"snapshot"`
	}{analyze.SchemaVersion, analyze.AnalyzerRevision, Fingerprint(cfg), manifest, identity})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Store persists one warm entry for root under dir. It returns the cache
// file path. The snapshot and stamps must come from the same pass over root.
func Store(dir, root string, cfg config.Config, snap analyze.Snapshot, stamps map[string]analyze.FileStamp) (string, error) {
	manifest := analyze.ManifestFromSnapshot(snap)
	if len(manifest) == 0 && len(snap.Files) != 0 {
		return "", fmt.Errorf("store cache: snapshot has no captured source manifest")
	}
	if len(stamps) == 0 && len(snap.Files) != 0 {
		return "", fmt.Errorf("store cache: snapshot has no captured metadata stamps")
	}
	entry := Entry{
		Version:           Version,
		SchemaVersion:     analyze.SchemaVersion,
		AnalyzerRevision:  analyze.AnalyzerRevision,
		Root:              snap.Root,
		BuiltAt:           time.Now().UTC(),
		ConfigFingerprint: Fingerprint(cfg),
		Snapshot:          snap,
		Stamps:            stamps,
		Manifest:          manifest,
	}
	if entry.Root == "" {
		entry.Root = root
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode cache entry: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create cache directory: %w", err)
	}
	path := filepath.Join(dir, EntryFile)
	if err := atomicfile.Write(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write cache entry: %w", err)
	}
	return path, nil
}

// LoadValidated loads a v2 entry and captures current source bytes once,
// validating every persisted content hash before returning it.
func LoadValidated(ctx context.Context, root, dir string, cfg config.Config) (analyze.Snapshot, Status, error) {
	path := filepath.Join(dir, EntryFile)
	status := Status{CachePath: path}
	data, err := os.ReadFile(path)
	if err != nil {
		status.Reason = "missing_cache"
		return analyze.Snapshot{}, status, fmt.Errorf("cache unavailable: %s", status.Reason)
	}
	status.Exists = true
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		status.Reason = "corrupt_cache"
		return analyze.Snapshot{}, status, fmt.Errorf("cache unavailable: %s", status.Reason)
	}
	status.Version, status.Root, status.TrackedFiles = entry.Version, entry.Root, len(entry.Stamps)
	if !entry.BuiltAt.IsZero() {
		built := entry.BuiltAt
		status.BuiltAt = &built
	}
	switch {
	case entry.Version != Version:
		status.Reason = "version_mismatch"
	case entry.SchemaVersion != analyze.SchemaVersion:
		status.Reason = "schema_mismatch"
	case entry.AnalyzerRevision != analyze.AnalyzerRevision:
		status.Reason = "analyzer_revision_mismatch"
	case entry.Root != root:
		status.Reason = "root_mismatch"
	case entry.ConfigFingerprint != Fingerprint(cfg):
		status.Reason = "config_changed"
	case len(entry.Manifest) == 0:
		status.Reason = "manifest_missing"
	default:
		status.Usable = true
	}
	if !status.Usable {
		return analyze.Snapshot{}, status, fmt.Errorf("cache unavailable: %s", status.Reason)
	}
	before, err := analyze.Stamps(ctx, root, cfg)
	if err != nil {
		status.Stale, status.Reason = true, "stamp_failed"
		return analyze.Snapshot{}, status, fmt.Errorf("cache unavailable: %s", status.Reason)
	}
	if stale, reason := stampsStale(entry.Stamps, before); stale {
		status.Stale, status.Reason = true, reason
		return analyze.Snapshot{}, status, fmt.Errorf("cache unavailable: %s", status.Reason)
	}
	contents := make(map[string][]byte, len(entry.Manifest))
	for p, want := range entry.Manifest {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			status.Stale, status.Reason = true, "content_unavailable"
			return analyze.Snapshot{}, status, fmt.Errorf("cache unavailable: %s", status.Reason)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != want {
			status.Stale, status.Reason = true, "content_changed"
			return analyze.Snapshot{}, status, fmt.Errorf("cache unavailable: %s", status.Reason)
		}
		contents[p] = b
	}
	after, err := analyze.Stamps(ctx, root, cfg)
	if err != nil || analyzeStampsChanged(before, after) {
		status.Stale, status.Reason = true, "repository_changed_during_capture"
		return analyze.Snapshot{}, status, fmt.Errorf("cache unavailable: %s", status.Reason)
	}
	snap := entry.Snapshot
	snap.Captured = contents
	snap.CapturedStamps = after
	if entry.Snapshot.Status.Snapshot == nil || entry.Snapshot.Status.Snapshot.ID == "" {
		status.Usable, status.Reason = false, "snapshot_identity_missing"
		return analyze.Snapshot{}, status, fmt.Errorf("cache unavailable: %s", status.Reason)
	}
	if got := SnapshotID(cfg, entry.Snapshot, entry.Manifest); got != entry.Snapshot.Status.Snapshot.ID {
		status.Usable, status.Reason = false, "snapshot_identity_mismatch"
		return analyze.Snapshot{}, status, fmt.Errorf("cache unavailable: %s", status.Reason)
	}
	snap.Status.Snapshot = &analyze.SnapshotInfo{ID: entry.Snapshot.Status.Snapshot.ID, Source: "cache", Freshness: "verified_at_start", CheckedAt: time.Now().UTC()}
	status.Reason = "fresh"
	return snap, status, nil
}

func analyzeStampsChanged(a, b map[string]analyze.FileStamp) bool {
	stale, _ := stampsStale(a, b)
	return stale
}

// Inspect reports whether a usable, fresh cache entry exists for root under
// dir. It builds a whole-tree identity stamp set to detect additions and
// removals as well as changes to previously recorded files.
func Inspect(ctx context.Context, root, dir string, cfg config.Config) Status {
	path := filepath.Join(dir, EntryFile)
	status := Status{CachePath: path}
	data, err := os.ReadFile(path)
	if err != nil {
		status.Reason = "missing_cache"
		return status
	}
	status.Exists = true
	var entry Entry
	if err = json.Unmarshal(data, &entry); err != nil {
		status.Reason = "corrupt_cache"
		return status
	}
	status.Version = entry.Version
	status.Root = entry.Root
	if !entry.BuiltAt.IsZero() {
		built := entry.BuiltAt
		status.BuiltAt = &built
	}
	status.TrackedFiles = len(entry.Stamps)
	switch {
	case entry.Version != Version:
		status.Reason = "version_mismatch"
		return status
	case entry.SchemaVersion != analyze.SchemaVersion:
		status.Reason = "schema_mismatch"
		return status
	case entry.AnalyzerRevision != analyze.AnalyzerRevision:
		status.Reason = "analyzer_revision_mismatch"
		return status
	case entry.Root != root:
		status.Reason = "root_mismatch"
		return status
	case entry.ConfigFingerprint != Fingerprint(cfg):
		status.Reason = "config_changed"
		return status
	}
	status.Usable = true
	current, err := analyze.Stamps(ctx, root, cfg)
	if err != nil {
		status.Stale = true
		status.Reason = "stamp_failed"
		return status
	}
	if stale, reason := stampsStale(entry.Stamps, current); stale {
		status.Stale = true
		status.Reason = reason
		return status
	}
	if len(entry.Manifest) == 0 {
		status.Usable = false
		status.Reason = "manifest_missing"
		return status
	}
	for p, want := range entry.Manifest {
		data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if readErr != nil {
			status.Stale, status.Reason = true, "content_unavailable"
			return status
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != want {
			status.Stale, status.Reason = true, "content_changed"
			return status
		}
	}
	status.Reason = "fresh"
	return status
}

// stampsStale compares recorded and current whole-tree identity stamps.
func stampsStale(recorded, current map[string]analyze.FileStamp) (bool, string) {
	for path, stamp := range recorded {
		currentStamp, ok := current[path]
		if !ok {
			return true, "tracked_file_missing"
		}
		if currentStamp.Size != stamp.Size {
			return true, "size_changed"
		}
		if !currentStamp.ModTime.Equal(stamp.ModTime) {
			return true, "mtime_changed"
		}
	}
	for path := range current {
		if _, ok := recorded[path]; !ok {
			return true, "tracked_file_added"
		}
	}
	return false, ""
}

// Clear removes the pan-owned entry under dir. It never touches other files
// in dir; a missing entry is an idempotent success.
func Clear(dir string) (ClearResult, error) {
	path := filepath.Join(dir, EntryFile)
	result := ClearResult{CachePath: path}
	if _, statErr := os.Stat(path); statErr == nil {
		if err := os.Remove(path); err != nil {
			return result, fmt.Errorf("remove cache entry: %w", err)
		}
		result.Removed = []string{path}
		return result, nil
	}
	// A missing entry is an idempotent success, not an error.
	result.Reason = "missing_cache"
	return result, nil
}
