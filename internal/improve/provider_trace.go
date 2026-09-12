package improve

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/dotcommander/pan/internal/provider"
)

type correctableProviderError struct{ error }

// ProviderToolTrace records a sanitized tool receipt. Summary traces keep only
// metadata. Full traces retain Result after redaction and the configured cap.
type ProviderToolTrace struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	ResultBytes int    `json:"result_bytes,omitempty"`
	Result      string `json:"result,omitempty"`
}

var (
	traceBearerSecret = regexp.MustCompile(`(?i)\bbearer\s+["']?[a-z0-9._~+/-]+`)
	traceQuotedSecret = regexp.MustCompile(`(?im)(\b(?:api[_-]?key|password|secret|token|authorization)\b(?:\\*["'])?\s*(?:=|:(?:=)?)\s*)(?:\\*["'])(?:\\.|[^\\\r\n])*?(?:\\*["'])`)
	traceBareSecret   = regexp.MustCompile(`(?im)(\b(?:api[_-]?key|password|secret|token|authorization)\b(?:\\*["'])?\s*(?:=|:(?:=)?)\s*)([^\s,;={}\[\]"'\\]+)`)
)

func (trace *ProviderTrace) recordTool(mode, name, result string, err error) {
	trace.ExplorationCallCount++
	event := ProviderToolTrace{Name: name}
	if err != nil {
		event.Status = "refused"
		trace.Tools = append(trace.Tools, event)
		return
	}
	event.Status = "ok"
	event.ResultBytes = len(result)
	if strings.EqualFold(mode, "full") {
		event.Result = scrubProviderText(result, 0)
	}
	trace.Tools = append(trace.Tools, event)
}

func (trace *ProviderTrace) capToolResults(limit int) {
	if limit <= 0 {
		return
	}
	for i := range trace.Tools {
		if trace.Tools[i].Result != "" {
			trace.Tools[i].Result = scrubProviderText(trace.Tools[i].Result, limit)
		}
	}
}

func scrubProviderText(value string, limit int) string {
	value = traceBearerSecret.ReplaceAllString(value, "[REDACTED]")
	value = traceQuotedSecret.ReplaceAllString(value, "${1}[REDACTED]")
	value = traceBareSecret.ReplaceAllString(value, "${1}[REDACTED]")
	return capUTF8Bytes(value, limit)

}

func capUTF8Bytes(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func isCorrectableProviderError(err error) bool {
	var invalid correctableProviderError
	return errors.As(err, &invalid)
}

func responseTrace(trace ProviderTrace, response provider.Response) ProviderTrace {
	trace.ResponseCount++
	trace.ResponseID = response.ID
	if response.Model != "" {
		trace.Model = response.Model
	}
	trace.PromptTokens += response.Usage.PromptTokens
	trace.CompletionTokens += response.Usage.CompletionTokens
	trace.TotalTokens += response.Usage.TotalTokens
	if len(response.Choices) == 1 {
		trace.FinishReason = response.Choices[0].FinishReason
	}
	return trace
}

func mergeProviderTrace(total, next ProviderTrace) ProviderTrace {
	if next.Model != "" {
		total.Model = next.Model
	}
	total.ResponseID = next.ResponseID
	total.FinishReason = next.FinishReason
	total.PromptTokens += next.PromptTokens
	total.CompletionTokens += next.CompletionTokens
	total.TotalTokens += next.TotalTokens
	total.ResponseCount += next.ResponseCount
	total.ExplorationCallCount += next.ExplorationCallCount
	total.AcceptedTerminalCount += next.AcceptedTerminalCount
	if next.TerminalTool != "" {
		total.TerminalTool = next.TerminalTool
	}
	total.Tools = append(total.Tools, next.Tools...)
	return total
}
