// Command check validates the bounded parity tracking artifacts without
// refreshing probes or changing the repository.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const (
	commandSchema   = "pan.source-command-map/v1"
	coverageSchema  = "pan.input-coverage/v1"
	receiptSchema   = "pan.input-receipt/v1"
	receiptSchemaV2 = "pan.input-receipt/v2"
	manifestSchema  = "pan.parity-manifest/v1"
)

type commandMap struct {
	Schema   string    `json:"schema"`
	Commands []command `json:"commands"`
}
type command struct {
	Source string   `json:"source"`
	Pan    string   `json:"pan"`
	Flags  []string `json:"flags"`
}
type coverage struct {
	Schema   string            `json:"schema"`
	Seed     string            `json:"seed"`
	Status   string            `json:"status"`
	Commands []coverageCommand `json:"commands"`
}
type coverageCommand struct {
	Source string  `json:"source"`
	Pan    string  `json:"pan"`
	Inputs []input `json:"inputs"`
}
type input struct {
	Name          string   `json:"name"`
	RequiredCases []string `json:"required_cases"`
	Status        string   `json:"status"`
	Evidence      string   `json:"evidence"`
}

type receipt struct {
	Schema             string            `json:"schema"`
	Contract           string            `json:"contract"`
	Manifest           string            `json:"manifest,omitempty"`
	ManifestSHA256     string            `json:"manifest_sha256,omitempty"`
	SnapshotRoots      []string          `json:"snapshot_roots,omitempty"`
	RetiredSourceRoots []string          `json:"retired_source_roots,omitempty"`
	Snapshot           map[string]string `json:"snapshot"`
	BinarySHA256       map[string]string `json:"binary_sha256"`
	Environment        string            `json:"environment_policy,omitempty"`
	Probes             []probe           `json:"probes"`
	Claims             []claim           `json:"claims,omitempty"`
}
type probe struct {
	Name           string   `json:"name"`
	SourceArgs     []string `json:"source_args"`
	PanArgs        []string `json:"pan_args"`
	SourceExitCode int      `json:"source_exit_code,omitempty"`
	PanExitCode    int      `json:"pan_exit_code,omitempty"`
	SourceOutput   string   `json:"source_output"`
	PanOutput      string   `json:"pan_output"`
	Assertion      string   `json:"assertion"`
}
type claim struct {
	Source       string `json:"source"`
	Pan          string `json:"pan"`
	Input        string `json:"input"`
	RequiredCase string `json:"required_case"`
	Probe        string `json:"probe"`
}
type manifest struct {
	Schema             string         `json:"schema"`
	Contract           string         `json:"contract"`
	SnapshotRoots      []string       `json:"snapshot_roots"`
	RetiredSourceRoots []string       `json:"retired_source_roots,omitempty"`
	Cases              []manifestCase `json:"cases"`
}
type manifestCase struct {
	ID        string  `json:"id"`
	Assertion string  `json:"assertion"`
	Claims    []claim `json:"claims"`
}
type binarySummary struct{ present, absent, mismatch int }
type activeClaim struct {
	receipt      string
	source       string
	pan          string
	input        string
	requiredCase string
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		fail(err)
	}
	if err := check(root); err != nil {
		fail(err)
	}
}

func check(root string) error {
	commands, err := readJSON[commandMap](filepath.Join(root, "testdata/parity/commands.json"))
	if err != nil {
		return err
	}
	if commands.Schema != commandSchema || len(commands.Commands) == 0 {
		return errors.New("commands.json has an invalid schema or empty command list")
	}
	covered, err := readJSON[coverage](filepath.Join(root, "testdata/parity/input-coverage.json"))
	if err != nil {
		return err
	}
	if covered.Schema != coverageSchema || covered.Seed != "commands.json" || covered.Status == "" {
		return errors.New("input-coverage.json has an invalid schema")
	}
	receiptPaths, active, err := checkCoverage(root, commands.Commands, covered.Commands)
	if err != nil {
		return err
	}
	var snapshotFiles, probes int
	var binaries binarySummary
	for _, path := range receiptPaths {
		r, err := readJSON[receipt](filepath.Join(root, "testdata/parity", filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		if err := checkReceipt(root, r); err != nil {
			return fmt.Errorf("receipt %q: %w", path, err)
		}
		if r.Schema == receiptSchemaV2 {
			if err := checkClaims(path, r, active); err != nil {
				return err
			}
		}
		if err := checkSnapshot(root, r.Snapshot, receiptRoots(r), r.RetiredSourceRoots); err != nil {
			return fmt.Errorf("receipt %q: %w", path, err)
		}
		snapshotFiles += len(r.Snapshot)
		probes += len(r.Probes)
		current := checkBinaries(r.BinarySHA256)
		binaries.present += current.present
		binaries.absent += current.absent
		binaries.mismatch += current.mismatch
	}
	fmt.Printf("tracking integrity passed: commands=%d inputs=%d receipts=%d snapshot_files=%d probes=%d\n", len(commands.Commands), inputCount(covered.Commands), len(receiptPaths), snapshotFiles, probes)
	fmt.Printf("binary hashes informational: present=%d absent=%d mismatch=%d\n", binaries.present, binaries.absent, binaries.mismatch)
	return nil
}

func checkCoverage(root string, commands []command, entries []coverageCommand) ([]string, []activeClaim, error) {
	expected := make(map[string]command, len(commands))
	for _, cmd := range commands {
		if cmd.Source == "" || cmd.Pan == "" {
			return nil, nil, errors.New("commands.json contains an empty source or pan command")
		}
		key := commandKey(cmd.Source, cmd.Pan)
		if _, ok := expected[key]; ok {
			return nil, nil, fmt.Errorf("commands.json repeats %q", key)
		}
		expected[key] = cmd
	}
	seen := make(map[string]bool, len(entries))
	receipts := map[string]bool{}
	var active []activeClaim
	for _, entry := range entries {
		key := commandKey(entry.Source, entry.Pan)
		if _, ok := expected[key]; !ok {
			return nil, nil, fmt.Errorf("input coverage has unmapped command %q", key)
		}
		if seen[key] {
			return nil, nil, fmt.Errorf("input coverage repeats command %q", key)
		}
		seen[key] = true
		if len(entry.Inputs) == 0 {
			return nil, nil, fmt.Errorf("input coverage has no inputs for %q", key)
		}
		seedInputs := map[string]bool{}
		for _, in := range entry.Inputs {
			if in.Name == "" || len(in.RequiredCases) == 0 {
				return nil, nil, fmt.Errorf("input coverage has an incomplete input for %q", key)
			}
			if seedInputs[in.Name] {
				return nil, nil, fmt.Errorf("input coverage repeats input %q for %q", in.Name, key)
			}
			seedInputs[in.Name] = true
			switch in.Status {
			case "unverified", "missing_in_pan":
			case "verified_bounded":
				r, err := checkEvidence(root, in.Evidence)
				if err != nil {
					return nil, nil, fmt.Errorf("input %q for %q: %w", in.Name, key, err)
				}
				receipts[in.Evidence] = true
				if r.Schema == receiptSchemaV2 {
					for _, required := range in.RequiredCases {
						active = append(active, activeClaim{in.Evidence, entry.Source, entry.Pan, in.Name, required})
					}
				}
			default:
				return nil, nil, fmt.Errorf("input %q for %q has invalid status %q", in.Name, key, in.Status)
			}
		}
		for _, flag := range expected[key].Flags {
			if !seedInputs["--"+flag] {
				return nil, nil, fmt.Errorf("input coverage is missing seed flag %q for %q", "--"+flag, key)
			}
		}
	}
	if len(seen) != len(expected) {
		missing := []string{}
		for key := range expected {
			if !seen[key] {
				missing = append(missing, key)
			}
		}
		sort.Strings(missing)
		return nil, nil, fmt.Errorf("input coverage is missing commands: %s", strings.Join(missing, ", "))
	}
	paths := make([]string, 0, len(receipts))
	for path := range receipts {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, active, nil
}

func checkEvidence(root, evidence string) (receipt, error) {
	var zero receipt
	if evidence == "" {
		return zero, errors.New("verified_bounded status requires evidence")
	}
	if !strings.HasPrefix(evidence, "receipts/") || !strings.HasSuffix(evidence, ".json") {
		return zero, fmt.Errorf("verified_bounded evidence must name a receipt JSON file: %q", evidence)
	}
	clean := filepath.Clean(filepath.FromSlash(evidence))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return zero, fmt.Errorf("evidence path escapes parity directory: %q", evidence)
	}
	path := filepath.Join(root, "testdata/parity", clean)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return zero, fmt.Errorf("evidence receipt does not resolve: %q", evidence)
	}
	r, err := readJSON[receipt](path)
	if err != nil || (r.Schema != receiptSchema && r.Schema != receiptSchemaV2) {
		return zero, fmt.Errorf("evidence receipt is invalid: %q", evidence)
	}
	return r, nil
}

func checkReceipt(root string, r receipt) error {
	if (r.Schema != receiptSchema && r.Schema != receiptSchemaV2) || len(r.Snapshot) == 0 {
		return errors.New("receipt has an invalid schema or empty snapshot")
	}
	if len(r.BinarySHA256) == 0 {
		return errors.New("receipt has no binary hashes")
	}
	if len(r.Probes) == 0 {
		return errors.New("receipt has no probes")
	}
	probes := map[string]probe{}
	for _, p := range r.Probes {
		if p.Name == "" || p.Name != strings.TrimSpace(p.Name) || !probeOutputsComplete(p) || p.Assertion == "" || len(p.SourceArgs) == 0 || len(p.PanArgs) == 0 {
			return fmt.Errorf("receipt probe %q is incomplete", p.Name)
		}
		if _, ok := probes[p.Name]; ok {
			return fmt.Errorf("receipt repeats probe %q", p.Name)
		}
		if r.Schema == receiptSchemaV2 {
			if err := checkAssertion(p); err != nil {
				return fmt.Errorf("receipt probe %q: %w", p.Name, err)
			}
		}
		probes[p.Name] = p
	}
	if r.Schema == receiptSchema {
		if err := checkLegacyContract(r, probes); err != nil {
			return err
		}
	} else {
		if err := checkManifest(root, r, probes); err != nil {
			return err
		}
		if len(r.Claims) == 0 {
			return errors.New("v2 receipt has no claims")
		}
	}
	for path, sum := range r.Snapshot {
		if err := validSHA256(sum); err != nil {
			return fmt.Errorf("snapshot hash for %q: %w", path, err)
		}
	}
	for path, sum := range r.BinarySHA256 {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("binary hash path is not absolute: %q", path)
		}
		if err := validSHA256(sum); err != nil {
			return fmt.Errorf("binary hash for %q: %w", path, err)
		}
	}
	return nil
}

func probeOutputsComplete(p probe) bool {
	switch p.Assertion {
	case "paired-exit-success", "paired-exit-failure":
		return true
	case "source-failure-pan-success":
		return p.PanOutput != ""
	default:
		return p.SourceOutput != "" && p.PanOutput != ""
	}
}

func checkAssertion(p probe) error {
	switch {
	case p.Assertion == "paired-exit-success":
		if p.SourceExitCode != 0 || p.PanExitCode != 0 {
			return errors.New("paired-exit-success requires zero exit codes")
		}
	case p.Assertion == "paired-exit-failure":
		if p.SourceExitCode == 0 || p.PanExitCode == 0 {
			return errors.New("paired-exit-failure requires nonzero exit codes")
		}
	case p.Assertion == "paired-success":
		if p.SourceExitCode != 0 || p.PanExitCode != 0 {
			return errors.New("paired-success requires zero exit codes")
		}
	case p.Assertion == "paired-failure":
		if p.SourceExitCode == 0 || p.PanExitCode == 0 {
			return errors.New("paired-failure requires nonzero exit codes")
		}
	case p.Assertion == "source-failure-pan-success":
		if p.SourceExitCode == 0 || p.PanExitCode != 0 {
			return errors.New("source-failure-pan-success requires a nonzero source exit and zero Pan exit")
		}
	case p.Assertion == "paired-json":
		if p.SourceExitCode != 0 || p.PanExitCode != 0 || !json.Valid([]byte(p.SourceOutput)) || !json.Valid([]byte(p.PanOutput)) {
			return errors.New("paired-json requires successful valid JSON output")
		}
	case strings.HasPrefix(p.Assertion, "paired-contains:"):
		needle := strings.TrimPrefix(p.Assertion, "paired-contains:")
		if needle == "" || !strings.Contains(p.SourceOutput, needle) || !strings.Contains(p.PanOutput, needle) {
			return errors.New("paired-contains lacks paired marker evidence")
		}
	default:
		return fmt.Errorf("unknown generic assertion %q", p.Assertion)
	}
	return nil
}

func checkLegacyContract(r receipt, probes map[string]probe) error {
	expected := map[string]bool{}
	switch r.Contract {
	case "repomap-endpoint/v1":
		for _, name := range []string{"list-text", "list-json", "route-text-default", "route-text-zero", "route-text-positive", "route-json-limited", "cwd-list-text", "cwd-list-json"} {
			expected[name] = true
		}
	case "repomap-map-default/v1":
		expected["default-text"] = true
	case "repomap-map-tokens/v1":
		for _, name := range []string{"tokens-default", "tokens-positive", "tokens-zero", "tokens-negative"} {
			expected[name] = true
		}
	case "repomap-map-formats/v1":
		for _, name := range []string{"format-enriched", "format-compact", "format-verbose", "format-detail", "format-lines", "format-xml"} {
			expected[name] = true
		}
	default:
		return fmt.Errorf("unknown legacy receipt contract %q", r.Contract)
	}
	if len(probes) != len(expected) {
		return fmt.Errorf("legacy receipt contract %q has unexpected probes", r.Contract)
	}
	for name := range expected {
		if _, ok := probes[name]; !ok {
			return fmt.Errorf("legacy receipt contract %q lacks probe %q", r.Contract, name)
		}
	}
	return nil
}

func checkManifest(root string, r receipt, probes map[string]probe) error {
	if r.Contract == "" || r.Manifest == "" || r.ManifestSHA256 == "" {
		return errors.New("v2 receipt lacks contract, manifest, or manifest_sha256")
	}
	if err := checkManifestPath(r.Manifest); err != nil {
		return err
	}
	path := filepath.Join(root, "testdata/parity", filepath.FromSlash(r.Manifest))
	got, err := fileSHA256(path)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	if got != r.ManifestSHA256 {
		return fmt.Errorf("manifest is stale or changed: %s", r.Manifest)
	}
	m, err := readJSON[manifest](path)
	if err != nil {
		return err
	}
	if m.Schema != manifestSchema || m.Contract != r.Contract || len(m.SnapshotRoots) == 0 {
		return errors.New("receipt manifest has invalid schema, contract, or roots")
	}
	if !sameStrings(m.SnapshotRoots, r.SnapshotRoots) {
		return errors.New("receipt snapshot_roots differ from manifest")
	}
	if !sameStrings(m.RetiredSourceRoots, r.RetiredSourceRoots) {
		return errors.New("receipt retired_source_roots differ from manifest")
	}
	if err := validateRetiredSourceRoots(r.SnapshotRoots, r.RetiredSourceRoots); err != nil {
		return err
	}
	if err := completeSnapshotRoots(r.SnapshotRoots); err != nil {
		return err
	}
	expected := map[string]manifestCase{}
	for _, c := range m.Cases {
		if c.ID == "" || c.Assertion == "" {
			return errors.New("manifest has incomplete case")
		}
		if _, ok := expected[c.ID]; ok {
			return fmt.Errorf("manifest repeats case %q", c.ID)
		}
		expected[c.ID] = c
	}
	for _, p := range probes {
		c, ok := expected[p.Name]
		if !ok {
			return fmt.Errorf("receipt probe %q is absent from manifest", p.Name)
		}
		if p.Assertion != c.Assertion {
			return fmt.Errorf("receipt probe %q assertion differs from manifest", p.Name)
		}
	}
	if len(probes) != len(expected) {
		return errors.New("receipt does not contain every manifest case")
	}
	wantClaims := map[string]string{}
	for _, c := range m.Cases {
		for _, cl := range c.Claims {
			if cl.Source == "" || cl.Pan == "" || cl.Input == "" || cl.RequiredCase == "" {
				return fmt.Errorf("manifest case %q has an incomplete claim", c.ID)
			}
			key := claimKey(cl.Source, cl.Pan, cl.Input, cl.RequiredCase)
			if _, ok := wantClaims[key]; ok {
				return fmt.Errorf("manifest repeats claim %q", key)
			}
			wantClaims[key] = c.ID
		}
	}
	gotClaims := map[string]bool{}
	for _, cl := range r.Claims {
		key := claimKey(cl.Source, cl.Pan, cl.Input, cl.RequiredCase)
		manifestCaseID, ok := wantClaims[key]
		if gotClaims[key] || !ok {
			return fmt.Errorf("receipt has an undeclared or repeated claim %q", key)
		}
		if cl.Probe != manifestCaseID {
			return fmt.Errorf("receipt claim %q names probe %q, want owning manifest case %q", key, cl.Probe, manifestCaseID)
		}
		gotClaims[key] = true
	}
	if len(gotClaims) != len(wantClaims) {
		return errors.New("receipt does not contain every manifest claim")
	}
	return nil
}
func checkClaims(path string, r receipt, active []activeClaim) error {
	claims := map[string]bool{}
	probes := map[string]probe{}
	for _, p := range r.Probes {
		probes[p.Name] = p
	}
	for _, c := range r.Claims {
		p, ok := probes[c.Probe]
		if c.Source == "" || c.Pan == "" || c.Input == "" || c.RequiredCase == "" || !ok {
			return fmt.Errorf("receipt %q has invalid claim", path)
		}
		if helpInvocation(p.SourceArgs) || helpInvocation(p.PanArgs) {
			return fmt.Errorf("receipt %q claim %q relies on a help-only probe %q", path, claimKey(c.Source, c.Pan, c.Input, c.RequiredCase), c.Probe)
		}
		key := claimKey(c.Source, c.Pan, c.Input, c.RequiredCase)
		if claims[key] {
			return fmt.Errorf("receipt %q repeats claim %q", path, key)
		}
		claims[key] = true
	}
	for _, a := range active {
		if a.receipt != path {
			continue
		}
		if !claims[claimKey(a.source, a.pan, a.input, a.requiredCase)] {
			return fmt.Errorf("receipt %q does not bind verified input %q (%s) for %s", path, a.input, commandKey(a.source, a.pan), a.requiredCase)
		}
	}
	return nil
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
func claimKey(source, pan, input, required string) string {
	return source + "\x00" + pan + "\x00" + input + "\x00" + required
}
func receiptRoots(r receipt) []string {
	if r.Schema == receiptSchemaV2 {
		return r.SnapshotRoots
	}
	return []string{"cmd", "internal", "assets", "testdata/http-go", ".work/repomap", "go.mod", "go.sum", "testdata/parity/commands.json"}
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
func checkManifestPath(path string) error {
	if !strings.HasPrefix(path, "manifests/") || !strings.HasSuffix(path, ".json") {
		return fmt.Errorf("manifest must name manifests JSON: %q", path)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("manifest path escapes parity directory: %q", path)
	}
	return nil
}
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func checkSnapshot(root string, recorded map[string]string, roots, retired []string) error {
	retiredSet := make(map[string]bool, len(retired))
	for _, path := range retired {
		retiredSet[path] = true
	}
	activeRoots := slices.DeleteFunc(slices.Clone(roots), func(path string) bool {
		if !retiredSet[path] {
			return false
		}
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		return errors.Is(err, fs.ErrNotExist)
	})
	if len(activeRoots) != len(roots) {
		recorded = maps.Clone(recorded)
		for path := range recorded {
			for retiredRoot := range retiredSet {
				if path == retiredRoot || strings.HasPrefix(path, retiredRoot+"/") {
					delete(recorded, path)
				}
			}
		}
	}
	actual, err := snapshot(root, activeRoots)
	if err != nil {
		return err
	}
	for path := range actual {
		if _, ok := recorded[path]; !ok {
			return snapshotDifference(actual, recorded)
		}
	}
	for path := range recorded {
		if _, ok := actual[path]; !ok {
			return snapshotDifference(actual, recorded)
		}
	}
	for path, hash := range actual {
		if recorded[path] != hash {
			return fmt.Errorf("snapshot is stale or changed: %s", path)
		}
	}
	return nil
}
func snapshot(root string, roots []string) (map[string]string, error) {
	if len(roots) == 0 {
		return nil, errors.New("snapshot has no roots")
	}
	files := map[string]string{}
	seenRoots := map[string]bool{}
	for _, rel := range roots {
		if err := validSnapshotRoot(rel); err != nil {
			return nil, err
		}
		if seenRoots[rel] {
			return nil, fmt.Errorf("snapshot repeats root %q", rel)
		}
		seenRoots[rel] = true
		full := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if err != nil {
			return nil, fmt.Errorf("snapshot root %q: %w", rel, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return nil, fmt.Errorf("snapshot root contains nonregular path %q", rel)
		}
		if !info.IsDir() {
			hash, err := fileSHA256(full)
			if err != nil {
				return nil, err
			}
			files[filepath.ToSlash(rel)] = hash
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
			hash, err := fileSHA256(path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(relative)] = hash
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk snapshot root %q: %w", rel, err)
		}
	}
	return files, nil
}
func validSnapshotRoot(root string) error {
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
	return fmt.Errorf("snapshot root is outside allowed parity roots: %q", root)
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
func snapshotDifference(actual, recorded map[string]string) error {
	var additions, removals []string
	for path := range actual {
		if _, ok := recorded[path]; !ok {
			additions = append(additions, path)
		}
	}
	for path := range recorded {
		if _, ok := actual[path]; !ok {
			removals = append(removals, path)
		}
	}
	sort.Strings(additions)
	sort.Strings(removals)
	return fmt.Errorf("snapshot membership changed: additions=%s removals=%s", strings.Join(additions, ","), strings.Join(removals, ","))
}
func checkBinaries(recorded map[string]string) binarySummary {
	var summary binarySummary
	for path, want := range recorded {
		got, err := fileSHA256(path)
		if errors.Is(err, fs.ErrNotExist) {
			summary.absent++
			continue
		}
		if err != nil || got != want {
			summary.mismatch++
			continue
		}
		summary.present++
	}
	return summary
}
func readJSON[T any](path string) (T, error) {
	var value T
	file, err := os.Open(path)
	if err != nil {
		return value, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return value, fmt.Errorf("decode %s: trailing JSON data", path)
	}
	return value, nil
}
func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func validSHA256(value string) error {
	if len(value) != sha256.Size*2 {
		return errors.New("must be a 64-character SHA-256 hex digest")
	}
	_, err := hex.DecodeString(value)
	return err
}
func commandKey(source, pan string) string { return source + " -> " + pan }
func inputCount(entries []coverageCommand) int {
	count := 0
	for _, entry := range entries {
		count += len(entry.Inputs)
	}
	return count
}
func fail(err error) { fmt.Fprintln(os.Stderr, "parity tracking invalid:", err); os.Exit(1) }
