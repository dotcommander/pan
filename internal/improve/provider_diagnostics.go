package improve

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/dotcommander/pan/internal/lsp"
)

type diagnosticResult struct {
	Status     string `json:"status"`
	Capability string `json:"capability"`
	File       string `json:"file"`
	Output     string `json:"output,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// lspDiagnostics asks gopls for diagnostics for the requested file. It is a
// separate CLI capability because the in-process LSP service only exposes
// navigation queries. The result remains a typed, bounded tool response.
func (r *providerReader) lspDiagnostics(ctx context.Context, full, rel string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	bin, err := exec.LookPath("gopls")
	if err != nil {
		return r.encodeDiagnostics(diagnosticResult{
			Status:     "unavailable",
			Capability: providerDiagnostics,
			File:       rel,
			Reason:     lsp.ReasonServerMissing,
			Detail:     "gopls is not installed",
		})
	}

	stdout := &boundedBuffer{limit: max(r.limit, 8<<10)}
	stderr := &boundedBuffer{limit: 8 << 10}
	runErr := runProviderCommand(ctx, providerCommand{path: bin, dir: r.root, args: []string{bin, "check", full}, stdout: stdout, stderr: stderr})
	if err := ctx.Err(); err != nil {
		return "", err
	}
	output := strings.TrimSpace(joinDiagnosticOutput(stdout.String(), stderr.String()))
	if runErr != nil {
		return r.encodeDiagnostics(diagnosticResult{
			Status:     "unavailable",
			Capability: providerDiagnostics,
			File:       rel,
			Output:     output,
			Reason:     lsp.ReasonQueryFailed,
			Detail:     boundedDiagnosticDetail(diagnosticFailureDetail(runErr, stderr.String())),
			Truncated:  stdout.truncated || stderr.truncated,
		})
	}
	return r.encodeDiagnostics(diagnosticResult{
		Status:     "ok",
		Capability: providerDiagnostics,
		File:       rel,
		Output:     output,
		Truncated:  stdout.truncated || stderr.truncated,
	})
}

func diagnosticFailureDetail(runErr error, stderr string) string {
	if strings.TrimSpace(stderr) == "" {
		return runErr.Error()
	}
	return runErr.Error() + ": " + strings.TrimSpace(stderr)
}

func (r *providerReader) encodeDiagnostics(result diagnosticResult) (string, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode diagnostics: %w", err)
	}
	return r.cap(string(encoded)), nil
}

func joinDiagnosticOutput(stdout, stderr string) string {
	if strings.TrimSpace(stdout) == "" {
		return stderr
	}
	if strings.TrimSpace(stderr) == "" {
		return stdout
	}
	return stdout + "\n" + stderr
}

func boundedDiagnosticDetail(value string) string {
	const limit = 512
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
