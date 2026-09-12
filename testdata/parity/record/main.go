// Command record executes a declared disposable parity manifest and writes an
// immutable v2 receipt. It never changes coverage status.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const manifestSchema = "pan.parity-manifest/v1"

type manifest struct {
	Schema             string         `json:"schema"`
	Contract           string         `json:"contract"`
	SnapshotRoots      []string       `json:"snapshot_roots"`
	RetiredSourceRoots []string       `json:"retired_source_roots,omitempty"`
	Cases              []manifestCase `json:"cases"`
}
type manifestCase struct {
	ID         string   `json:"id"`
	CWD        string   `json:"cwd"`
	SourceArgs []string `json:"source_args"`
	PanArgs    []string `json:"pan_args"`
	SourceExit int      `json:"source_exit"`
	PanExit    int      `json:"pan_exit"`
	Assertion  string   `json:"assertion"`
	Claims     []claim  `json:"claims"`
}
type claim struct {
	Source       string `json:"source"`
	Pan          string `json:"pan"`
	Input        string `json:"input"`
	RequiredCase string `json:"required_case"`
	Probe        string `json:"probe,omitempty"`
}
type receipt struct {
	Schema             string            `json:"schema"`
	Contract           string            `json:"contract"`
	Manifest           string            `json:"manifest"`
	ManifestSHA256     string            `json:"manifest_sha256"`
	SnapshotRoots      []string          `json:"snapshot_roots"`
	RetiredSourceRoots []string          `json:"retired_source_roots,omitempty"`
	Snapshot           map[string]string `json:"snapshot"`
	BinarySHA256       map[string]string `json:"binary_sha256"`
	Environment        string            `json:"environment_policy"`
	Probes             []probe           `json:"probes"`
	Claims             []claim           `json:"claims"`
}
type probe struct {
	Name           string   `json:"name"`
	SourceArgs     []string `json:"source_args"`
	PanArgs        []string `json:"pan_args"`
	SourceExitCode int      `json:"source_exit_code"`
	PanExitCode    int      `json:"pan_exit_code"`
	SourceOutput   string   `json:"source_output"`
	PanOutput      string   `json:"pan_output"`
	Assertion      string   `json:"assertion"`
}

func main() {
	manifestPath := flag.String("manifest", "", "parity manifest path relative to testdata/parity")
	sourceBin := flag.String("source-bin", "", "absolute source executable")
	panBin := flag.String("pan-bin", "", "absolute Pan executable")
	receiptPath := flag.String("receipt", "", "new receipt path relative to testdata/parity")
	flag.Parse()
	if err := record(*manifestPath, *sourceBin, *panBin, *receiptPath); err != nil {
		fmt.Fprintln(os.Stderr, "parity record failed:", err)
		os.Exit(1)
	}
}

func record(manifestPath, sourceBin, panBin, receiptPath string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := relativeUnder(manifestPath, "manifests/"); err != nil {
		return err
	}
	if err := relativeUnder(receiptPath, "receipts/"); err != nil {
		return err
	}
	if !strings.HasSuffix(manifestPath, ".json") || !strings.HasSuffix(receiptPath, ".json") {
		return errors.New("manifest and receipt must be JSON files")
	}
	for _, bin := range []string{sourceBin, panBin} {
		if !filepath.IsAbs(bin) {
			return fmt.Errorf("binary path must be absolute: %q", bin)
		}
		info, err := os.Stat(bin)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("binary is not a regular file: %q", bin)
		}
	}
	fullManifest := filepath.Join(root, "testdata/parity", filepath.FromSlash(manifestPath))
	m, err := readJSON[manifest](fullManifest)
	if err != nil {
		return err
	}
	if m.Schema != manifestSchema || m.Contract == "" || len(m.SnapshotRoots) == 0 || len(m.Cases) == 0 {
		return errors.New("manifest has invalid schema, contract, roots, or cases")
	}
	if err := completeSnapshotRoots(m.SnapshotRoots); err != nil {
		return err
	}
	manifestHash, err := fileSHA256(fullManifest)
	if err != nil {
		return err
	}
	if err := validateRetiredSourceRoots(m.SnapshotRoots, m.RetiredSourceRoots); err != nil {
		return err
	}
	snapshot, err := snapshot(root, m.SnapshotRoots, m.RetiredSourceRoots)
	if err != nil {
		return err
	}
	stateRoot, err := os.MkdirTemp("", "pan-parity-record-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stateRoot)
	r := receipt{Schema: "pan.input-receipt/v2", Contract: m.Contract, Manifest: manifestPath, ManifestSHA256: manifestHash, SnapshotRoots: m.SnapshotRoots, RetiredSourceRoots: m.RetiredSourceRoots, Snapshot: snapshot, BinarySHA256: map[string]string{}, Environment: "isolated-v1", Claims: []claim{}}
	for _, bin := range []string{sourceBin, panBin} {
		sum, err := fileSHA256(bin)
		if err != nil {
			return err
		}
		r.BinarySHA256[bin] = sum
	}
	ids := map[string]bool{}
	claims := map[string]bool{}
	for caseIndex, c := range m.Cases {
		if c.ID == "" || ids[c.ID] || c.Assertion == "" || len(c.SourceArgs) == 0 || len(c.PanArgs) == 0 || len(c.Claims) == 0 {
			return fmt.Errorf("manifest case %q is incomplete or repeated", c.ID)
		}
		if claimedHelpInvocation(c) {
			return fmt.Errorf("manifest case %q claims verified behavior from a help-only probe", c.ID)
		}
		ids[c.ID] = true
		cwd, err := caseCWD(root, c.CWD)
		if err != nil {
			return fmt.Errorf("case %q: %w", c.ID, err)
		}
		caseState := filepath.Join(stateRoot, fmt.Sprintf("%03d", caseIndex))
		sourceOutput, sourceExit, err := run(context.Background(), sourceBin, c.SourceArgs, cwd, filepath.Join(caseState, "source"))
		if err != nil {
			return fmt.Errorf("case %q source: %w", c.ID, err)
		}
		panOutput, panExit, err := run(context.Background(), panBin, c.PanArgs, cwd, filepath.Join(caseState, "pan"))
		if err != nil {
			return fmt.Errorf("case %q pan: %w", c.ID, err)
		}
		if sourceExit != c.SourceExit || panExit != c.PanExit {
			return fmt.Errorf("case %q exit codes source=%d pan=%d; want source=%d pan=%d", c.ID, sourceExit, panExit, c.SourceExit, c.PanExit)
		}
		if err := checkAssertion(c.Assertion, sourceExit, panExit, sourceOutput, panOutput); err != nil {
			return fmt.Errorf("case %q: %w", c.ID, err)
		}
		r.Probes = append(r.Probes, probe{Name: c.ID, SourceArgs: c.SourceArgs, PanArgs: c.PanArgs, SourceExitCode: sourceExit, PanExitCode: panExit, SourceOutput: sourceOutput, PanOutput: panOutput, Assertion: c.Assertion})
		for _, cl := range c.Claims {
			if cl.Source == "" || cl.Pan == "" || cl.Input == "" || cl.RequiredCase == "" {
				return fmt.Errorf("case %q has incomplete claim", c.ID)
			}
			cl.Probe = c.ID
			key := cl.Source + "\x00" + cl.Pan + "\x00" + cl.Input + "\x00" + cl.RequiredCase
			if claims[key] {
				return fmt.Errorf("manifest repeats claim %q", key)
			}
			claims[key] = true
			r.Claims = append(r.Claims, cl)
		}
	}
	out := filepath.Join(root, "testdata/parity", filepath.FromSlash(receiptPath))
	if _, err := os.Lstat(out); err == nil {
		return fmt.Errorf("refusing to overwrite immutable receipt %q", receiptPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	encoded, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(out, encoded, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s: cases=%d claims=%d snapshot_files=%d\n", receiptPath, len(r.Probes), len(r.Claims), len(r.Snapshot))
	return nil
}

func validateRetiredSourceRoots(roots, retired []string) error {
	available := make(map[string]bool, len(roots))
	for _, root := range roots {
		available[root] = true
	}
	for _, root := range retired {
		if !strings.HasPrefix(root, ".work/") || !available[root] {
			return fmt.Errorf("retired source root must name a declared .work root: %q", root)
		}
	}
	return nil
}

func checkAssertion(assertion string, sourceExit, panExit int, sourceOutput, panOutput string) error {
	switch {
	case assertion == "paired-exit-success":
		if sourceExit != 0 || panExit != 0 {
			return errors.New("paired-exit-success requires zero exit codes")
		}
	case assertion == "paired-exit-failure":
		if sourceExit == 0 || panExit == 0 {
			return errors.New("paired-exit-failure requires nonzero exit codes")
		}
	case assertion == "paired-success":
		if sourceExit != 0 || panExit != 0 {
			return errors.New("paired-success requires zero exit codes")
		}
	case assertion == "paired-failure":
		if sourceExit == 0 || panExit == 0 {
			return errors.New("paired-failure requires nonzero exit codes")
		}
	case assertion == "source-failure-pan-success":
		if sourceExit == 0 || panExit != 0 {
			return errors.New("source-failure-pan-success requires a nonzero source exit and zero Pan exit")
		}
	case assertion == "paired-json":
		if sourceExit != 0 || panExit != 0 || !json.Valid([]byte(sourceOutput)) || !json.Valid([]byte(panOutput)) {
			return errors.New("paired-json requires successful valid JSON output")
		}
	case strings.HasPrefix(assertion, "paired-contains:"):
		needle := strings.TrimPrefix(assertion, "paired-contains:")
		if needle == "" || !strings.Contains(sourceOutput, needle) || !strings.Contains(panOutput, needle) {
			return errors.New("paired-contains lacks paired marker evidence")
		}
	default:
		return fmt.Errorf("unknown generic assertion %q", assertion)
	}
	return nil
}

func claimedHelpInvocation(c manifestCase) bool {
	return len(c.Claims) > 0 && (helpInvocation(c.SourceArgs) || helpInvocation(c.PanArgs))
}

func helpInvocation(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--help", "-h", "-help", "--usage", "help":
			return true
		}
		if strings.HasPrefix(arg, "--help=") || strings.HasPrefix(arg, "-help=") || strings.HasPrefix(arg, "--usage=") {
			return true
		}
	}
	return false
}

func run(ctx context.Context, bin string, args []string, cwd, stateRoot string) (string, int, error) {
	for _, dir := range []string{stateRoot, filepath.Join(stateRoot, "config"), filepath.Join(stateRoot, "cache"), filepath.Join(stateRoot, "data"), filepath.Join(stateRoot, "state"), filepath.Join(stateRoot, "tmp")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", 0, err
		}
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = cwd
	cmd.Env = isolatedEnvironment(stateRoot)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(output), exit.ExitCode(), nil
	}
	return "", 0, err
}

func isolatedEnvironment(stateRoot string) []string {
	env := make([]string, 0, 10)
	for _, key := range []string{"PATH", "LANG", "LC_ALL", "TZ"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return append(env,
		"HOME="+stateRoot,
		"XDG_CONFIG_HOME="+filepath.Join(stateRoot, "config"),
		"XDG_CACHE_HOME="+filepath.Join(stateRoot, "cache"),
		"XDG_DATA_HOME="+filepath.Join(stateRoot, "data"),
		"XDG_STATE_HOME="+filepath.Join(stateRoot, "state"),
		"TMPDIR="+filepath.Join(stateRoot, "tmp"),
	)
}
func caseCWD(root, cwd string) (string, error) {
	if cwd == "" {
		return root, nil
	}
	if filepath.IsAbs(cwd) {
		return "", errors.New("case cwd must be repository-relative")
	}
	clean := filepath.Clean(filepath.FromSlash(cwd))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid case cwd %q", cwd)
	}
	full := filepath.Join(root, clean)
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("case cwd is not a directory: %q", cwd)
	}
	return full, nil
}
func relativeUnder(path, prefix string) error {
	if path == "" || !strings.HasPrefix(path, prefix) {
		return fmt.Errorf("path must be under %s: %q", prefix, path)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes parity directory: %q", path)
	}
	return nil
}
func snapshot(root string, roots, retiredRoots []string) (map[string]string, error) {
	files := map[string]string{}
	seen := map[string]bool{}
	retired := make(map[string]bool, len(retiredRoots))
	for _, rel := range retiredRoots {
		retired[rel] = true
	}
	for _, rel := range roots {
		if err := validRoot(rel); err != nil {
			return nil, err
		}
		if seen[rel] {
			return nil, fmt.Errorf("snapshot repeats root %q", rel)
		}
		seen[rel] = true
		full := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if errors.Is(err, fs.ErrNotExist) && retired[rel] {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&fs.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return nil, fmt.Errorf("snapshot root contains nonregular path %q", rel)
		}
		if !info.IsDir() {
			sum, err := fileSHA256(full)
			if err != nil {
				return nil, err
			}
			files[filepath.ToSlash(rel)] = sum
			continue
		}
		err = filepath.WalkDir(full, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() && entry.Name() == ".git" {
				return filepath.SkipDir
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() {
				return fmt.Errorf("snapshot root contains nonregular path %q", path)
			}
			sum, err := fileSHA256(path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(relative)] = sum
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}
func validRoot(root string) error {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(root)))
	if clean != root || clean == "." || strings.HasPrefix(clean, "../") || filepath.IsAbs(root) {
		return fmt.Errorf("invalid snapshot root %q", root)
	}
	for _, prefix := range []string{"cmd", "internal", "assets", "testdata", ".work"} {
		if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
			return nil
		}
	}
	if root == "go.mod" || root == "go.sum" {
		return nil
	}
	return fmt.Errorf("snapshot root outside allowed parity roots: %q", root)
}

func completeSnapshotRoots(roots []string) error {
	need := map[string]bool{"cmd": false, "internal": false, "go.mod": false, "go.sum": false, "testdata/parity/commands.json": false}
	hasSource, hasFixture := false, false
	for _, root := range roots {
		if _, ok := need[root]; ok {
			need[root] = true
		}
		hasSource = hasSource || strings.HasPrefix(root, ".work/")
		hasFixture = hasFixture || strings.HasPrefix(root, "testdata/") && root != "testdata/parity/commands.json"
	}
	for root, present := range need {
		if !present {
			return fmt.Errorf("v2 snapshot_roots lacks required root %q", root)
		}
	}
	if !hasSource || !hasFixture {
		return errors.New("v2 snapshot_roots must include a source tree and a fixture root")
	}
	return nil
}
func readJSON[T any](path string) (T, error) {
	var v T
	f, err := os.Open(path)
	if err != nil {
		return v, err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	if err := d.Decode(&v); err != nil {
		return v, err
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return v, errors.New("trailing JSON data")
	}
	return v, nil
}
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var _ = sort.Strings
