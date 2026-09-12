package cli

import (
	"context"
	"fmt"
	"slices"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/agent"
	"github.com/dotcommander/pan/internal/eval"
	"github.com/dotcommander/pan/internal/review"
	"github.com/dotcommander/pan/internal/scan"
)

// outputCatalogSchema is stamped on the catalog result.
const outputCatalogSchema = "pan.output-catalog/v1"

// Catalog literals shared by several surfaces, hoisted to constants so
// the repeated schema, media type, and flag spellings cannot drift.
const (
	envelopeSchema    = "pan/v1"
	jsonEnvelopeMedia = "json envelope"
	applicationJSON   = "application/json"
	flagJSON          = "--json"
	flagRepo          = "--repo"
	flagFormat        = "--format"
	flagCull          = "--cull"
	flagOutput        = "--output"
	evalPrivacy       = "aggregates only; no paths, identities, or input filenames"
	evalTopKLimit     = "top_k fixed at 15"
	flagReport        = "--report"
	flagOutcomes      = "--outcomes"
)

// agentProtocolErrors returns the stable `agent stdio` error codes.
func agentProtocolErrors() []string {
	return []string{
		agent.CodeInvalidRequest,
		agent.CodeUnsupportedSchema,
		agent.CodeUnsupportedOp,
		agent.CodeRequestTooLarge,
		agent.CodeScanFailed,
		agent.CodeCanceled,
	}
}

// agentServeErrors returns the JSON-RPC error codes `agent serve` answers
// with, rendered as stable strings for the output catalog.
func agentServeErrors() []string {
	return []string{
		"-32700 parse error",
		"-32600 invalid request",
		"-32601 method not found",
		"-32602 invalid params",
		"-32000 server error",
	}
}

// OutputSurface describes one pan output surface: what produces it, its
// schema and media type, what it is best for, its privacy posture, and its
// stability contract.
type OutputSurface struct {
	Name          string   `json:"name"`
	Producer      string   `json:"producer"`
	Schema        string   `json:"schema,omitempty"`
	MediaType     string   `json:"media_type"`
	BestFor       string   `json:"best_for"`
	Privacy       string   `json:"privacy"`
	Limits        []string `json:"limits,omitempty"`
	Flags         []string `json:"flags,omitempty"`
	AgentOps      []string `json:"agent_operations,omitempty"`
	StableErrors  []string `json:"stable_errors,omitempty"`
	Compatibility string   `json:"compatibility"`
}

// OutputCatalog is the deterministic inventory of pan's output surfaces.
// It is command metadata only: building it inspects no repository.
type OutputCatalog struct {
	Schema   string          `json:"schema"`
	Surfaces []OutputSurface `json:"surfaces"`
}

// reportSurfaces returns the envelope and composed-report surfaces.
func reportSurfaces() []OutputSurface {
	return []OutputSurface{
		{
			Name: "envelope", Producer: "standard command results (except explicit document and protocol modes)", Schema: envelopeSchema,
			MediaType: "text or json (--format)", BestFor: "machine-readable command results",
			Privacy:       "repository paths and bounded analysis metadata",
			Flags:         []string{flagRepo, flagFormat},
			Compatibility: "existing pan/v1 envelope fields remain unchanged",
		},
		{
			Name: "review-report-markdown", Producer: "review report --markdown", Schema: review.DocumentSchema,
			MediaType: "text/markdown", BestFor: "human first read of the composed audit report",
			Privacy:       "repository paths and deterministic evidence reasons",
			Limits:        []string{"read queue bounded at 100 rows", "--top caps the queue further"},
			Flags:         []string{"--top", flagCull, "--markdown", flagOutput},
			Compatibility: "opt-in body format; default envelope output remains unchanged",
		},
		{
			Name: "review-report-json", Producer: "review report --json", Schema: review.DocumentSchema,
			MediaType: applicationJSON, BestFor: "automation and review eval report inputs",
			Privacy:       "repository paths, scores, lanes, and content identities",
			Limits:        []string{"read queue bounded at 100 rows", "--top caps the queue further"},
			Flags:         []string{"--top", flagJSON, flagOutput},
			Compatibility: "document fields are the eval report contract",
		},
		{
			Name: "cull-ledger", Producer: "review report --cull", Schema: review.CullLedgerSchema,
			MediaType: "embedded report section", BestFor: "separating production review rows from test, docs, generated, and low-signal lanes",
			Privacy:       "repository paths and deterministic lane reasons",
			Flags:         []string{flagCull},
			Compatibility: "read-queue rows always carry their lane; --cull only appends the ledger",
		},
	}
}

// evalSurfaces returns the review-eval output surfaces.
func evalSurfaces() []OutputSurface {
	return []OutputSurface{
		{
			Name: "eval", Producer: "review eval --json", Schema: eval.Schema,
			MediaType: applicationJSON, BestFor: "source-compatible deterministic evaluation automation",
			Privacy:       evalPrivacy,
			Limits:        []string{evalTopKLimit},
			Flags:         []string{flagReport, flagOutcomes, flagJSON, flagOutput},
			Compatibility: "source output name alias for eval-json; pan.eval/v1 remains the output schema",
		},
		{
			Name: "eval-json", Producer: "review eval --json", Schema: eval.Schema,
			MediaType: applicationJSON, BestFor: "deterministic evaluation automation",
			Privacy:       evalPrivacy,
			Limits:        []string{evalTopKLimit},
			Flags:         []string{flagReport, flagOutcomes, flagJSON, flagOutput},
			Compatibility: "aggregate fields are the evaluation contract",
		},
		{
			Name: "eval-markdown", Producer: "review eval --markdown",
			MediaType: "text/markdown", BestFor: "human inspection of evaluation health",
			Privacy:       evalPrivacy,
			Limits:        []string{evalTopKLimit},
			Flags:         []string{flagReport, flagOutcomes, "--markdown", flagOutput},
			Compatibility: "opt-in presentation over the unchanged evaluation calculation",
		},
	}
}

// agentSurfaces returns the machine-protocol surfaces.
func agentSurfaces() []OutputSurface {
	return []OutputSurface{
		{
			Name: "agent-stdio", Producer: "agent stdio", Schema: agent.Schema,
			MediaType: "application/x-ndjson", BestFor: "bounded agent sessions over one repository",
			Privacy:  "report rows carry repository paths; no provider contact, no repository mutation",
			Limits:   []string{"1 MiB request cap", "serial operations"},
			Flags:    []string{flagRepo},
			AgentOps: agent.OperationNames(), StableErrors: agentProtocolErrors(),
			Compatibility: "hello and report operations only",
		},
		{
			Name: "agent-serve", Producer: "agent serve", Schema: "jsonrpc-2.0/ndjson",
			MediaType: "application/x-ndjson", BestFor: "JSON-RPC agent sessions with per-method results",
			Privacy:  "results carry repository paths; no provider contact, no repository mutation",
			Limits:   []string{"1 MiB request cap", "symbols top bounded at 500", "one bounded snapshot per session"},
			Flags:    []string{flagRepo},
			AgentOps: agent.ServeMethodNames(), StableErrors: agentServeErrors(),
			Compatibility: "Pan and Pan-compatible methods listed in agent_operations",
		},
		{
			Name: "agent-schema", Producer: "agent schema", Schema: agent.SchemaDocumentSchema,
			MediaType: jsonEnvelopeMedia, BestFor: "machine discovery of both agent protocol contracts",
			Privacy:       "protocol metadata only; no repository access",
			Flags:         []string{flagFormat},
			Compatibility: "derived from live protocol constants; cannot drift from code",
		},
	}
}

// integrationSurfaces returns the language-server, configuration, and
// readiness surfaces.
func integrationSurfaces() []OutputSurface {
	return []OutputSurface{
		{
			Name: "lsp-status", Producer: "context lsp status", Schema: envelopeSchema,
			MediaType: jsonEnvelopeMedia, BestFor: "discovering which language servers a repository needs",
			Privacy:       "language names, workspace roots, and command names; no file contents",
			Limits:        []string{"detection is local: no server is started"},
			Flags:         []string{flagRepo, flagFormat},
			Compatibility: "typed missing-server rows list every tried command",
		},
		{
			Name: "lsp-query", Producer: "context lsp refs|def|hover|symbols", Schema: envelopeSchema,
			MediaType: jsonEnvelopeMedia, BestFor: "one bounded language-server query with typed capability results",
			Privacy:       "query positions and bounded results; servers run as local subprocesses",
			Limits:        []string{"200 locations, 500 symbols, 4000 hover chars", "20s per-query timeout"},
			Flags:         []string{flagRepo, flagFormat},
			Compatibility: "unavailable answers carry stable reasons, never fabricated results",
		},
		{
			Name: "config-init", Producer: "config init", Schema: envelopeSchema,
			MediaType: jsonEnvelopeMedia, BestFor: "seeding the user configuration from embedded defaults",
			Privacy:       "configuration path and byte count only",
			Limits:        []string{"refuses an existing file unless --force"},
			Flags:         []string{"--path", "--force"},
			Compatibility: "skipped is a reported outcome, not an error",
		},
		{
			Name: "config-print", Producer: "config print", Schema: envelopeSchema,
			MediaType: jsonEnvelopeMedia, BestFor: "inspecting effective normalized limits and policy",
			Privacy:       "effective values and their source path; no secrets are stored in configuration",
			Flags:         []string{flagFormat},
			Compatibility: "mirrors the config.yaml schema with defaults filled in",
		},
		{
			Name: doctorCommand, Producer: "scan doctor", Schema: scan.DoctorSchema,
			MediaType: jsonEnvelopeMedia, BestFor: "safe local readiness checks",
			Privacy:       "configuration source and analysis health; optional environment presence only, never credentials",
			Limits:        []string{"local analysis; optional loopback models check without inference or redirects"},
			Flags:         []string{flagRepo, flagFormat, flagJSON, "--model", "--base-url", "--api-key-env"},
			Compatibility: "new read-only surface",
		},
	}
}

// catalogSurface returns the self-describing catalog surface.
func catalogSurface() []OutputSurface {
	return []OutputSurface{
		{
			Name: "output-catalog", Producer: "review outputs", Schema: outputCatalogSchema,
			MediaType: jsonEnvelopeMedia, BestFor: "discovering pan output choices",
			Privacy:       "command metadata only",
			Flags:         []string{flagFormat},
			Compatibility: "new discovery surface",
		},
	}
}

// BuildOutputCatalog returns the full output surface inventory.
func BuildOutputCatalog() OutputCatalog {
	return OutputCatalog{
		Schema:   outputCatalogSchema,
		Surfaces: slices.Concat(reportSurfaces(), evalSurfaces(), agentSurfaces(), integrationSurfaces(), pipelineSurfaces(), catalogSurface()),
	}
}

// ReviewOutputsCmd is `pan review outputs`: the deterministic inventory of
// pan's output surfaces, optionally narrowed to one named surface.
type ReviewOutputsCmd struct {
	Surface string `arg:"" optional:"" help:"Report only this output surface."`
}

// Run executes `pan review outputs`, narrowing to one surface when named.
func (c ReviewOutputsCmd) Run(kctx *kong.Context, root *Root, deps Deps, _ context.Context) error {
	catalog := BuildOutputCatalog()
	if c.Surface != "" {
		found := false
		for _, surface := range catalog.Surfaces {
			if surface.Name == c.Surface {
				catalog.Surfaces = []OutputSurface{surface}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unknown output surface %q", c.Surface)
		}
	}
	return emitResult(kctx, root, deps, displayRepo(root.Repo), catalog)
}
