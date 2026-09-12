// Package provideracceptance runs hash-bound, read-only acceptance probes for
// Pan's provider-backed improve workflow.
package provideracceptance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	improveconfig "github.com/dotcommander/pan/internal/improve/config"
)

const Schema = "pan.provider-acceptance/v1"

var fixtureNames = []string{"obvious", "none", "ambiguous"}

type Executor func(context.Context, string, []string) ([]byte, []byte, error)

type Options struct {
	Binary, Config, Fixtures, ReceiptDir, Repo string
	Provider, Model                            string
	Live                                       bool
	Execute                                    Executor
}

type Case struct {
	Name                   string          `json:"name"`
	ExpectedClassification string          `json:"expected_classification"`
	FinalClassification    string          `json:"final_classification,omitempty"`
	Status                 string          `json:"status"`
	Terminal               string          `json:"terminal,omitempty"`
	CredentialFreeArgv     []string        `json:"argv"`
	Result                 json.RawMessage `json:"result,omitempty"`
	Assertions             []string        `json:"assertions"`
	Checked                []string        `json:"checked,omitempty"`
	Limitations            []string        `json:"limitations,omitempty"`
	AffectedFiles          []string        `json:"affected_files,omitempty"`
	AffectedSymbols        []string        `json:"affected_symbols,omitempty"`
	Stderr                 string          `json:"stderr,omitempty"`
	ResponseCount          int             `json:"response_count"`
	ExplorationCallCount   int             `json:"exploration_call_count"`
	AcceptedTerminalCount  int             `json:"accepted_terminal_count"`
	Tokens                 int             `json:"tokens"`
	OutputBytes            int             `json:"output_bytes"`
	reportedModel          string
}

type Receipt struct {
	Schema                string    `json:"schema"`
	StartedAt             time.Time `json:"started_at"`
	ElapsedMillis         int64     `json:"elapsed_millis"`
	Binary                string    `json:"binary"`
	Config                string    `json:"config"`
	Fixtures              string    `json:"fixtures"`
	BinarySHA256          string    `json:"binary_sha256"`
	ConfigSHA256          string    `json:"config_sha256"`
	FixturesSHA256        string    `json:"fixtures_sha256"`
	Head                  string    `json:"head"`
	DirtyDiffSHA256       string    `json:"dirty_diff_sha256"`
	Provider              string    `json:"provider,omitempty"`
	RequestedModel        string    `json:"requested_model,omitempty"`
	ReportedModel         string    `json:"reported_model,omitempty"`
	Status                string    `json:"status"`
	Cases                 []Case    `json:"cases"`
	ResponseCount         int       `json:"response_count"`
	ExplorationCallCount  int       `json:"exploration_call_count"`
	AcceptedTerminalCount int       `json:"accepted_terminal_count"`
	Tokens                int       `json:"tokens"`
	OutputBytes           int       `json:"output_bytes"`
}

func Run(ctx context.Context, o Options) (Receipt, error) {
	if o.Binary == "" || o.Config == "" {
		return Receipt{}, errors.New("binary and config are required")
	}
	if o.Fixtures == "" {
		o.Fixtures = filepath.Join("testdata", "provider_acceptance")
	}
	if o.ReceiptDir == "" {
		o.ReceiptDir = filepath.Join(".work", "provider-acceptance", "runs")
	}
	if o.Repo == "" {
		o.Repo = "."
	}
	cfg, err := improveconfig.Load(o.Config)
	if err != nil {
		return Receipt{}, fmt.Errorf("validate improve config: %w", err)
	}
	if o.Provider == "" {
		o.Provider = cfg.Provider
	}
	if o.Model == "" {
		o.Model = cfg.Model
	}
	if o.Execute == nil {
		o.Execute = execute
	}
	started := time.Now()
	r := Receipt{Schema: Schema, StartedAt: started, Binary: o.Binary, Config: o.Config, Fixtures: o.Fixtures, Provider: o.Provider, RequestedModel: o.Model, Status: "failed"}
	if r.BinarySHA256, err = fileHash(o.Binary); err != nil {
		return r, err
	}
	if r.ConfigSHA256, err = fileHash(o.Config); err != nil {
		return r, err
	}
	if r.FixturesSHA256, err = treeHash(o.Fixtures); err != nil {
		return r, err
	}
	if r.Head, r.DirtyDiffSHA256, err = gitState(ctx, o.Repo); err != nil {
		return r, err
	}
	var runErr error
	for _, name := range fixtureNames {
		caseResult, caseErr := runCase(ctx, o, name)
		r.Cases = append(r.Cases, caseResult)
		r.ResponseCount += caseResult.ResponseCount
		r.ExplorationCallCount += caseResult.ExplorationCallCount
		r.AcceptedTerminalCount += caseResult.AcceptedTerminalCount
		r.Tokens += caseResult.Tokens
		r.OutputBytes += caseResult.OutputBytes
		if caseResult.reportedModel != "" {
			if r.ReportedModel != "" && r.ReportedModel != caseResult.reportedModel {
				runErr = errors.Join(runErr, fmt.Errorf("%s: reported model %q differs from earlier %q", name, caseResult.reportedModel, r.ReportedModel))
			} else {
				r.ReportedModel = caseResult.reportedModel
			}
		}
		if caseErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("%s: %w", name, caseErr))
		}
	}
	if o.Live && r.ReportedModel != r.RequestedModel {
		runErr = errors.Join(runErr, fmt.Errorf("reported model %q, want requested model %q", r.ReportedModel, r.RequestedModel))
	}
	r.ElapsedMillis = time.Since(started).Milliseconds()
	if runErr == nil {
		r.Status = "passed"
	}
	if err := writeReceipt(o.ReceiptDir, started, r); err != nil {
		return r, errors.Join(runErr, err)
	}
	return r, runErr
}

func runCase(ctx context.Context, o Options, name string) (Case, error) {
	fixture := filepath.Join(o.Fixtures, name)
	before, err := treeHash(fixture)
	want := "no_candidate"
	if name == "obvious" {
		want = "success"
	}
	c := Case{Name: name, ExpectedClassification: want, Status: "failed"}
	if err != nil {
		return c, err
	}
	args := []string{"--repo", fixture, "--format", "json", "improve", "probe"}
	if o.Live {
		args = append(args, "--config", o.Config, "--trace", "summary")
	} else {
		args = append(args, "--trace", "off")
	}
	c.CredentialFreeArgv = append([]string{o.Binary}, args...)
	out, stderr, runErr := o.Execute(ctx, o.Binary, args)
	if json.Valid(out) {
		c.Result = append(json.RawMessage(nil), out...)
	}
	c.Stderr = sanitize(string(stderr), 4096)
	c.OutputBytes = len(out)
	if runErr == nil {
		runErr = parseProbe(out, fixture, &c, o.Live)
	}
	after, hashErr := treeHash(fixture)
	if hashErr != nil {
		runErr = errors.Join(runErr, hashErr)
	} else if after != before {
		runErr = errors.Join(runErr, errors.New("fixture bytes changed"))
	} else {
		c.Assertions = append(c.Assertions, "fixture bytes unchanged")
	}
	if runErr == nil {
		c.Status = "passed"
	}
	return c, runErr
}

func parseProbe(data []byte, fixture string, c *Case, live bool) error {
	var envelope struct {
		SchemaVersion string `json:"schema_version"`
		Analysis      struct {
			Complete bool `json:"complete"`
		} `json:"analysis"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode pan envelope: %w", err)
	}
	if envelope.SchemaVersion != "pan/v1" || !envelope.Analysis.Complete {
		return fmt.Errorf("incomplete or unexpected pan envelope: schema=%q complete=%t", envelope.SchemaVersion, envelope.Analysis.Complete)
	}
	c.Assertions = append(c.Assertions, "pan/v1 complete envelope")
	var probe struct {
		Schema  string `json:"schema"`
		Success bool   `json:"success"`
		Outcome string `json:"outcome"`
		DryRun  bool   `json:"dry_run"`
		Applied bool   `json:"applied"`
		Trace   *struct {
			Model                 string `json:"model"`
			ResponseCount         int    `json:"response_count"`
			ExplorationCallCount  int    `json:"exploration_call_count"`
			AcceptedTerminalCount int    `json:"accepted_terminal_count"`
			PromptTokens          int    `json:"prompt_tokens"`
			CompletionTokens      int    `json:"completion_tokens"`
			TotalTokens           int    `json:"total_tokens"`
		} `json:"trace_receipt"`
		Proposal struct {
			Changes []struct {
				FilePath    string `json:"file_path"`
				NewContents string `json:"new_contents"`
			} `json:"changes"`
			Candidates []struct {
				Name string `json:"name"`
			} `json:"candidates"`
			Checked     []string `json:"checked"`
			Limitations []string `json:"limitations"`
		} `json:"proposal"`
	}
	if err := json.Unmarshal(envelope.Result, &probe); err != nil {
		return fmt.Errorf("decode improve probe: %w", err)
	}
	if probe.Schema != "pan.improve-probe/v1" || !probe.Success || !probe.DryRun || probe.Applied {
		return fmt.Errorf("invalid probe contract: schema=%q success=%t dry_run=%t applied=%t", probe.Schema, probe.Success, probe.DryRun, probe.Applied)
	}
	c.Assertions = append(c.Assertions, "pan.improve-probe/v1 success", "dry_run=true", "applied=false")
	c.Terminal = probe.Outcome
	c.FinalClassification = probe.Outcome
	c.Checked = probe.Proposal.Checked
	c.Limitations = probe.Proposal.Limitations
	for _, change := range probe.Proposal.Changes {
		c.AffectedFiles = append(c.AffectedFiles, change.FilePath)
	}
	for _, candidate := range probe.Proposal.Candidates {
		c.AffectedSymbols = append(c.AffectedSymbols, candidate.Name)
	}
	if len(c.AffectedSymbols) == 0 {
		for _, change := range probe.Proposal.Changes {
			removed, err := removedGoSymbols(filepath.Join(fixture, filepath.FromSlash(change.FilePath)), []byte(change.NewContents))
			if err != nil {
				return fmt.Errorf("derive affected symbols for %s: %w", change.FilePath, err)
			}
			c.AffectedSymbols = append(c.AffectedSymbols, removed...)
		}
	}
	if probe.Trace != nil {
		c.ResponseCount = probe.Trace.ResponseCount
		c.ExplorationCallCount = probe.Trace.ExplorationCallCount
		c.AcceptedTerminalCount = probe.Trace.AcceptedTerminalCount
		c.Tokens = probe.Trace.TotalTokens
		if c.Tokens == 0 {
			c.Tokens = probe.Trace.PromptTokens + probe.Trace.CompletionTokens
		}
		c.reportedModel = probe.Trace.Model
	}
	if c.FinalClassification != c.ExpectedClassification {
		return fmt.Errorf("classification %q, want %q", c.FinalClassification, c.ExpectedClassification)
	}
	if live && c.AcceptedTerminalCount != 1 {
		return fmt.Errorf("accepted terminal count %d, want 1", c.AcceptedTerminalCount)
	}
	if c.Name == "obvious" {
		if strings.Join(c.AffectedFiles, ",") != "main.go" || strings.Join(c.AffectedSymbols, ",") != "unusedHelper" {
			return fmt.Errorf("affected files/symbols %v/%v, want main.go/unusedHelper", c.AffectedFiles, c.AffectedSymbols)
		}
	} else if len(c.AffectedFiles) != 0 || len(c.AffectedSymbols) != 0 || len(c.Checked) == 0 {
		return errors.New("no-candidate result lacks checked evidence or invents affected targets")
	}
	return nil
}

func removedGoSymbols(originalPath string, replacement []byte) ([]string, error) {
	original, err := os.ReadFile(originalPath)
	if err != nil {
		return nil, err
	}
	before, err := declaredGoSymbols(originalPath, original)
	if err != nil {
		return nil, err
	}
	after, err := declaredGoSymbols(originalPath, replacement)
	if err != nil {
		return nil, err
	}
	var removed []string
	for name := range before {
		if !after[name] {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	return removed, nil
}

func declaredGoSymbols(filename string, data []byte) (map[string]bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), filename, data, 0)
	if err != nil {
		return nil, err
	}
	result := make(map[string]bool)
	for _, decl := range file.Decls {
		switch value := decl.(type) {
		case *ast.FuncDecl:
			result[value.Name.Name] = true
		case *ast.GenDecl:
			for _, spec := range value.Specs {
				switch item := spec.(type) {
				case *ast.TypeSpec:
					result[item.Name.Name] = true
				case *ast.ValueSpec:
					for _, name := range item.Names {
						result[name.Name] = true
					}
				}
			}
		}
	}
	return result, nil
}

func VerifyReceipt(ctx context.Context, path string, o Options) error {
	var receipt Receipt
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		return err
	}
	if receipt.Schema != Schema || receipt.Status != "passed" {
		return fmt.Errorf("receipt is not a passed %s receipt", Schema)
	}
	if o.Binary == "" {
		o.Binary = receipt.Binary
	}
	if o.Config == "" {
		o.Config = receipt.Config
	}
	if o.Fixtures == "" {
		o.Fixtures = receipt.Fixtures
	}
	if o.Repo == "" {
		o.Repo = "."
	}
	actual := make(map[string]string)
	if actual["binary"], err = fileHash(o.Binary); err != nil {
		return err
	}
	if actual["config"], err = fileHash(o.Config); err != nil {
		return err
	}
	if actual["fixtures"], err = treeHash(o.Fixtures); err != nil {
		return err
	}
	if actual["HEAD"], actual["dirty diff"], err = gitState(ctx, o.Repo); err != nil {
		return err
	}
	want := map[string]string{"binary": receipt.BinarySHA256, "config": receipt.ConfigSHA256, "fixtures": receipt.FixturesSHA256, "HEAD": receipt.Head, "dirty diff": receipt.DirtyDiffSHA256}
	for _, label := range []string{"binary", "config", "fixtures", "HEAD", "dirty diff"} {
		if actual[label] != want[label] {
			return fmt.Errorf("stale receipt: %s mismatch", label)
		}
	}
	return nil
}

func execute(ctx context.Context, binary string, args []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func writeReceipt(dir string, started time.Time, receipt Receipt) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, started.UTC().Format("20060102T150405.000000000Z")+".json")
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func treeHash(root string) (string, error) {
	hash := sha256.New()
	var paths []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	for _, path := range paths {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		hash.Write([]byte(relative))
		hash.Write([]byte{0})
		hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func gitState(ctx context.Context, repo string) (string, string, error) {
	head, err := commandOutput(ctx, repo, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	diff, err := commandOutput(ctx, repo, "git", "diff", "--no-ext-diff", "--binary", "HEAD")
	if err != nil {
		return "", "", err
	}
	untracked, err := commandOutput(ctx, repo, "git", "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", "", err
	}
	hash := sha256.New()
	hash.Write(diff)
	for _, relative := range strings.Split(string(untracked), "\x00") {
		if relative == "" {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(repo, relative))
		if readErr != nil {
			return "", "", readErr
		}
		hash.Write([]byte(relative))
		hash.Write([]byte{0})
		hash.Write(data)
	}
	return strings.TrimSpace(string(head)), hex.EncodeToString(hash.Sum(nil)), nil
}

func commandOutput(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return output, nil
}

var secretPattern = regexp.MustCompile(`(?i)(api[_-]?key|authorization|token|secret|password)(\s*[:=]\s*)[^\s,;]+`)

func sanitize(value string, limit int) string {
	value = secretPattern.ReplaceAllString(value, `${1}${2}[REDACTED]`)
	if len(value) > limit {
		value = value[:limit] + "…"
	}
	return value
}
