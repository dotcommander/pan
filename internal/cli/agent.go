package cli

import (
	"bufio"
	"context"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/agent"
)

// AgentCmd groups machine protocols for coding agents. Stdio implements
// the sequential JSONL protocol, Serve implements the JSON-RPC 2.0 NDJSON
// service, and Schema dumps the bounded machine-readable contract for
// both surfaces. No session contacts providers or mutates the target repository.
// Stdio feedback can append to an explicitly selected local outcome ledger.
type AgentCmd struct {
	Stdio  AgentStdioCmd  `cmd:"" help:"Sequential JSONL protocol (hello, scan, query, context, feedback, report)."`
	Serve  AgentServeCmd  `cmd:"" help:"JSON-RPC 2.0 NDJSON service over stdio (map, symbol, file, status, overview, report)."`
	Schema AgentSchemaCmd `cmd:"" help:"Machine-readable schema dump for both agent protocols."`
}

// AgentStdioCmd is `pan agent stdio`.
type AgentStdioCmd struct {
	Outcomes string `name:"outcomes" type:"path" help:"Optional local eval outcome ledger; enables feedback writes."`
}

// Run executes `pan agent stdio`.
func (c AgentStdioCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	var outcomes agent.OutcomeWriter
	if c.Outcomes != "" {
		outcomes = agent.AppendOutcome(c.Outcomes)
	}
	state := deps.App.AgentServe(root.Repo)
	return agent.RunWithSession(ctx, bufio.NewReader(deps.In), deps.Out, agent.Session{Build: state.AgentReport, Context: state.AgentContext, Verify: state.AgentVerify, Outcomes: outcomes})
}

// AgentServeCmd is `pan agent serve`: the JSON-RPC 2.0 service over NDJSON
// stdio. Each request uses current verified evidence, reusing unchanged
// snapshots; requests are capped, answer stable JSON-RPC error codes, and
// never mutate the target repository.
type AgentServeCmd struct{}

// Run executes `pan agent serve`.
func (AgentServeCmd) Run(kctx *kong.Context, root *Root, deps Deps, ctx context.Context) error {
	return agent.RunServe(ctx, bufio.NewReader(deps.In), deps.Out, deps.App.AgentServe(root.Repo))
}

// AgentSchemaCmd is `pan agent schema`: the deterministic contract dump
// for the stdio and serve protocols. It inspects no repository.
type AgentSchemaCmd struct{}

// Run executes `pan agent schema`.
func (AgentSchemaCmd) Run(kctx *kong.Context, root *Root, deps Deps) error {
	return emitResult(kctx, root, deps, displayRepo(root.Repo), agent.BuildSchema())
}
