// Package cli defines pan's Kong command taxonomy: global flags, command
// groups, input validation, envelope emission, and exit-code mapping.
// Implemented commands delegate to the app service layer and preserve
// bounded, local analysis by default.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/app"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/pipeline/buildinfo"
	"github.com/dotcommander/pan/internal/render"
)

// Documented process exit codes (see README.md).
const (
	ExitSuccess = 0 // command completed
	ExitFailure = 1 // usage, validation, or runtime error
	ExitConfig  = 2 // configuration could not be loaded
)

// binaryVersion is the human-readable pan binary version.
var binaryVersion = "v0.1.0"

const (
	formatJSON     = "json"
	outputKey      = "output"
	formatKey      = "format"
	homeCommand    = "home"
	serveCmd       = "serve"
	doctorCommand  = "doctor"
	versionCommand = "version"
	scanCommand    = "scan"
)

// Root is the pan application model: global flags plus the command groups.
type Root struct {
	VersionFlag    kong.VersionFlag `name:"version" short:"v" help:"Print binary and analysis schema version and quit."`
	Repo           string           `name:"repo" default:"." type:"path" env:"PAN_REPO" help:"Target repository root; maps the source audit directory selector."`
	AllRepos       bool             `name:"all" help:"Explicitly allow analysis of a parent directory containing multiple repositories."`
	Standalone     bool             `name:"standalone" help:"Explicitly analyze a directory that is not a Git repository."`
	Format         string           `name:"format" default:"text" enum:"text,json" help:"Output format for command results; maps the source audit JSON selector."`
	Artifact       string           `name:"artifact" type:"path" help:"Write command output atomically to this file instead of stdout."`
	SnapshotSource string           `name:"snapshot-source" enum:"auto,live" default:"auto" help:"Snapshot source policy."`

	Context  ContextCmd  `cmd:"" help:"Task-oriented code retrieval and maps."`
	Flow     FlowCmd     `cmd:"" help:"Execution, dependency, route, and pipeline evidence."`
	Scan     ScanCmd     `cmd:"" help:"Repository inventory, risk, hygiene, and surface discovery."`
	Review   ReviewCmd   `cmd:"" help:"Structured audit packets and reports."`
	Clean    CleanCmd    `cmd:"" help:"Cleanup planning and confirmed application."`
	Improve  ImproveCmd  `cmd:"" help:"Guarded test and refactor workflows; dry-run by default."`
	Agent    AgentCmd    `cmd:"" help:"Machine protocols for coding agents."`
	Cache    CacheCmd    `cmd:"" help:"Incremental-analysis cache lifecycle."`
	Config   ConfigCmd   `cmd:"" help:"Configuration bootstrap and validation."`
	Commands CommandsCmd `cmd:"" help:"Show the complete command catalog."`
	Home     HomeCmd     `cmd:"" default:"1" hidden:""`

	Brief   BriefAliasCmd  `cmd:"" help:"Compatibility alias for: pan context brief."`
	Impact  ImpactAliasCmd `cmd:"" help:"Compatibility alias for: pan flow impact."`
	Version VersionCmd     `cmd:"" help:"Print binary and analysis schema version."`
}

// Deps carries the runtime collaborators injected into command Run methods.
// In is the request stream for machine-protocol commands such as
// `agent stdio`; commands that do not read requests ignore it.
type Deps struct {
	App                app.Service
	Out                io.Writer
	In                 io.Reader
	LoadConfig         func() (config.Config, error)
	LoadReadOnlyConfig func() (config.Config, error)
}

// Run parses args against the taxonomy and executes the selected command.
func Run(ctx context.Context, args []string, deps Deps) error {
	root := &Root{}
	var exitCode *int
	parser, err := kong.New(root,
		kong.Name("pan"),
		kong.Description("Repository evidence and guarded improvement workflows for coding agents."),
		kong.UsageOnError(),
		kong.Help(panHelpPrinter),
		kong.Writers(deps.Out, deps.Out),
		kong.Exit(func(code int) {
			exitCode = &code
		}),
		kong.Vars{"version": fmt.Sprintf("pan %s schema %s", binaryVersion, analyze.SchemaVersion)},
		kong.BindTo(ctx, (*context.Context)(nil)),
	)
	if err != nil {
		return err
	}
	kctx, err := parser.Parse(args)
	if exitCode != nil {
		if *exitCode == 0 {
			return nil
		}
		return &ExitError{Code: *exitCode, Err: err}
	}
	if err != nil {
		return err
	}
	if requiresRepositoryBoundary(commandPath(kctx)) {
		if err := prepareRepositoryTarget(root, args, os.Getenv("PAN_REPO")); err != nil {
			return err
		}
	}
	loader := deps.LoadConfig
	if slices.Equal(commandPath(kctx), []string{scanCommand, doctorCommand}) && deps.LoadReadOnlyConfig != nil {
		loader = deps.LoadReadOnlyConfig
	}
	if loader != nil && !metadataCommand(commandPath(kctx)) {
		cfg, err := loader()
		if err != nil {
			return &ExitError{Code: ExitConfig, Err: err}
		}
		deps.App = app.New(app.Deps{Config: cfg, SnapshotSource: root.SnapshotSource})
	}
	runCtx, cancel := commandContext(ctx, commandPath(kctx), deps.App.EffectiveConfig().Config.CommandTimeout)
	defer cancel()
	kctx.BindTo(runCtx, (*context.Context)(nil))
	if root.Artifact == "" {
		return kctx.Run(deps)
	}
	var artifact bytes.Buffer
	artifactDeps := deps
	artifactDeps.Out = &artifact
	if err := kctx.Run(artifactDeps); err != nil {
		return err
	}
	return writeContextArtifact(root.Artifact, artifact.Bytes())
}

func requiresRepositoryBoundary(path []string) bool {
	if metadataCommand(path) {
		return false
	}
	return !slices.Equal(path, []string{"flow", "render"}) &&
		!slices.Equal(path, []string{"flow", "validate"})
}

// HomeCmd renders the human-first repository dashboard when Pan is invoked
// without a command. It is hidden because it is an entry experience, not a
// command users need to learn.
type HomeCmd struct{}

// Run renders a metadata-only entry screen. A bare invocation must never walk,
// analyze, or otherwise touch the selected repository.
func (HomeCmd) Run(root *Root, deps Deps, _ context.Context) error {
	return writeHome(deps.Out, root.Repo)
}

// CommandsCmd exposes Kong's complete command tree without making it the root
// help experience.
type CommandsCmd struct{}

// Run prints every leaf command and its short description.
func (CommandsCmd) Run(kctx *kong.Context, deps Deps) error {
	return writeCommandCatalog(deps.Out, kctx.Model)
}

func metadataCommand(path []string) bool {
	return len(path) == 1 && (path[0] == versionCommand || path[0] == homeCommand)
}

// commandContext keeps streaming commands alive until their caller cancels
// them. Other commands retain the configured execution deadline.
func commandContext(parent context.Context, path []string, timeout time.Duration) (context.Context, context.CancelFunc) {
	if isLongLivedCommand(path) {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}

func isLongLivedCommand(path []string) bool {
	return slices.Equal(path, []string{"flow", serveCmd}) ||
		slices.Equal(path, []string{"agent", serveCmd}) ||
		slices.Equal(path, []string{"agent", "stdio"})
}

// ExitError is an error carrying an explicit process exit code.
type ExitError struct {
	Code int
	Err  error
}

// Error returns the wrapped error's message.
func (e *ExitError) Error() string { return e.Err.Error() }

// Unwrap exposes the carried error for errors.Is and errors.As.
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode maps err onto the documented exit codes. nil maps to success;
// unmapped errors map to the generic error code.
func ExitCode(err error) int {
	if err == nil {
		return ExitSuccess
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return ExitFailure
}

// commandPath returns the selected command path, for example
// []string{"flow", "calls"}. Positional arguments are never included.
func commandPath(kctx *kong.Context) []string {
	var path []string
	for node := kctx.Selected(); node != nil && node.Type == kong.CommandNode; node = node.Parent {
		path = append(path, node.Name)
	}
	slices.Reverse(path)
	return path
}

// emit renders the standard versioned envelope for an implemented command
// derived from one analysis snapshot, using the root's selected format.
func emit(kctx *kong.Context, root *Root, deps Deps, snap analyze.Snapshot, result any) error {
	envelope := render.NewEnvelope(commandPath(kctx), snap.Root, snap.Status, result, snap.Diagnostics)
	return render.Write(deps.Out, root.Format, envelope)
}

// emitResult renders the standard versioned envelope for implemented
// commands whose result is not derived from an analysis snapshot.
func emitResult(kctx *kong.Context, root *Root, deps Deps, repository string, result any) error {
	envelope := render.NewEnvelope(commandPath(kctx), repository, analyze.Status{Complete: true}, result, nil)
	return render.Write(deps.Out, root.Format, envelope)
}

// emitDiagnostics renders the standard envelope for an implemented command
// that reports per-finding validation failures instead of aborting.
func emitDiagnostics(kctx *kong.Context, root *Root, deps Deps, result any, messages []string) error {
	diagnostics := make([]analyze.Diagnostic, 0, len(messages))
	for _, message := range messages {
		diagnostics = append(diagnostics, analyze.Diagnostic{Level: "error", Message: message})
	}
	envelope := render.NewEnvelope(commandPath(kctx), displayRepo(root.Repo), analyze.Status{}, result, diagnostics)
	return render.Write(deps.Out, root.Format, envelope)
}

// VersionCmd prints the binary and analysis schema version.
type VersionCmd struct {
	JSON bool `name:"json" help:"Emit binary version, schema version, and build provenance as JSON."`
}

// Run prints the binary and analysis schema version.
func (c VersionCmd) Run(deps Deps) error {
	if c.JSON {
		return json.NewEncoder(deps.Out).Encode(struct {
			Version string `json:"version"`
			Schema  string `json:"schema_version"`
			buildinfo.Info
		}{Version: binaryVersion, Schema: analyze.SchemaVersion, Info: buildinfo.Read()})
	}
	_, err := fmt.Fprintf(deps.Out, "pan %s schema %s\n", binaryVersion, analyze.SchemaVersion)
	return err
}
