package retrieval

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/ranking"
)

// Task packet bounds. Every cap below emits a truncation record when hit.
const (
	taskDefaultTokens = 4096
	taskTargetLimit   = 6
	taskSymbolCap     = 3
	taskConsumerCap   = 5
	taskTestCap       = 5
	taskImportCap     = 5
	taskRelationCap   = 19
	taskEvidenceCap   = 8
	taskReadNextCap   = 5
	taskSourceLines   = 60
	taskMaxSources    = 3
)

// TaskOptions shapes one task packet build.
type TaskOptions struct {
	// Tokens bounds the encoded packet; <= 0 uses the default.
	Tokens int
	// Consumed are repo-relative paths already in the agent's context; they
	// rank up but their source is omitted from the packet.
	Consumed []string
	// PolicyID records the retrieval policy that produced the ranking. Empty
	// preserves the stable structural/lexical default.
	PolicyID string
}

// TaskEvidence is one matched fact linking the goal to a target file.
type TaskEvidence struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// TaskRelationship is one structural edge from a target to another file.
type TaskRelationship struct {
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	Provenance string `json:"provenance"`
}

// TaskSource is one bounded source excerpt embedded for an unread target.
type TaskSource struct {
	Symbol string       `json:"symbol"`
	Lines  []SourceLine `json:"lines"`
}

// TaskTarget is one file selected as relevant to the goal.
type TaskTarget struct {
	Path       string             `json:"path"`
	Package    string             `json:"package,omitempty"`
	Confidence string             `json:"confidence"`
	Symbols    []analyze.Symbol   `json:"symbols,omitempty"`
	Evidence   []TaskEvidence     `json:"evidence,omitempty"`
	Relations  []TaskRelationship `json:"relationships,omitempty"`
	Consumers  []string           `json:"consumers,omitempty"`
	Tests      []string           `json:"tests,omitempty"`
	Imports    []string           `json:"imports,omitempty"`
	Risk       string             `json:"risk"`
	Parse      string             `json:"parse"`
	Source     []TaskSource       `json:"source,omitempty"`
	Consumed   bool               `json:"consumed,omitempty"`
}

// TaskReport is the goal-oriented context packet.
type TaskReport struct {
	Goal             string               `json:"goal"`
	Budget           TaskBudget           `json:"budget"`
	Selection        TaskSelection        `json:"selection"`
	Rules            []string             `json:"rules,omitempty"`
	Targets          []TaskTarget         `json:"targets"`
	ReadNext         []analyze.ReadNext   `json:"read_next,omitempty"`
	VerifyCommands   []string             `json:"verify_commands,omitempty"`
	FollowUpCommands []string             `json:"follow_up_commands,omitempty"`
	Truncations      []analyze.Truncation `json:"truncations,omitempty"`
}

// TaskBudget reports the token bound and the encoded size of the packet.
type TaskBudget struct {
	MaxTokens  int `json:"max_tokens"`
	UsedTokens int `json:"used_tokens"`
}

// TaskSelection reports how targets were chosen.
type TaskSelection struct {
	PolicyID string `json:"policy_id"`
	Strategy string `json:"strategy"`
	Limit    int    `json:"limit"`
	Selected int    `json:"selected"`
}

// Task builds a bounded packet: positively matched terms get a chance at a
// complete target before remaining ranked candidates. Every trial charges the
// final encoded packet, including derived fields and truncation disclosures.
func Task(snap analyze.Snapshot, ranked []ranking.RankedFile, goal string, opts TaskOptions) (TaskReport, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return TaskReport{}, errors.New("task goal must not be blank")
	}
	if opts.Tokens < 0 {
		return TaskReport{}, errors.New("task tokens must not be negative")
	}
	if opts.Tokens == 0 {
		opts.Tokens = taskDefaultTokens
	}
	if opts.PolicyID == "" {
		opts.PolicyID = StructuralLexicalPolicy
	}
	consumed, err := NormalizeConsumed(snap.Root, opts.Consumed)
	if err != nil {
		return TaskReport{}, err
	}
	report := TaskReport{
		Goal:      goal,
		Budget:    TaskBudget{MaxTokens: opts.Tokens},
		Selection: TaskSelection{PolicyID: opts.PolicyID, Strategy: "goal-term-coverage/v2", Limit: taskTargetLimit},
		Rules:     snap.Instructions,
	}
	candidates := taskCandidates(ranked, snap, goal)
	if err := packTaskTargets(&report, candidates, snap, ranked, consumed); err != nil {
		return TaskReport{}, err
	}
	if err := finalizeTaskReport(&report); err != nil {
		return TaskReport{}, err
	}
	if report.Budget.UsedTokens > opts.Tokens {
		return TaskReport{}, fmt.Errorf("task token budget %d cannot encode report schema", opts.Tokens)
	}
	return report, nil
}

func (r *TaskReport) addTruncation(field string, shown, total int, reason string) {
	if total > shown {
		r.Truncations = append(r.Truncations, analyze.Truncation{Field: field, Shown: shown, Total: total, Reason: reason})
	}
}

// taskTokens estimates the encoded size of the report as ceil(bytes / 4).
func taskTokens(report TaskReport) int {
	data, err := json.Marshal(report)
	if err != nil {
		return 0
	}
	return (len(data) + 3) / 4
}

// setTaskUsedTokens records the encoded report size after its own budget field
// is present. The value can change the JSON size, so converge before enforcing
// the caller's hard budget.
func setTaskUsedTokens(report *TaskReport) error {
	for range 4 {
		used := taskTokens(*report)
		if used == 0 {
			return errors.New("encode task report")
		}
		if report.Budget.UsedTokens == used {
			return nil
		}
		report.Budget.UsedTokens = used
	}
	return errors.New("task report token count did not converge")
}

// lexicalCallerFiles lists files calling a target's uniquely named symbol,
// or one of its exact typed declarations. Colliding lexical names are omitted.
func lexicalCallerFiles(snap analyze.Snapshot, target ranking.RankedFile) []string {
	byName := make(map[string][]analyze.Symbol)
	for _, symbol := range target.Symbols {
		byName[symbol.Name] = append(byName[symbol.Name], symbol)
	}
	occurrences := make(map[string]int)
	for _, symbol := range snap.Symbols {
		occurrences[symbol.Name]++
	}
	seen := make(map[string]struct{})
	var out []string
	for _, edge := range snap.Edges {
		if edge.Kind != edgeKindCalls || edge.Location.Path == target.Path {
			continue
		}
		for _, symbol := range byName[edge.To] {
			if edge.Target != nil {
				if edge.Confidence != analyze.ConfidenceConfirmed || !edge.CallsSymbol(symbol) {
					continue
				}
			} else if edge.Confidence != analyze.ConfidenceLexical || occurrences[edge.To] > 1 {
				continue
			}
			if _, dup := seen[edge.Location.Path]; !dup {
				seen[edge.Location.Path] = struct{}{}
				out = append(out, edge.Location.Path)
			}
			break
		}
	}
	slices.Sort(out)
	return out
}

// NormalizeConsumed canonicalizes consumed paths against root: relative
// slash paths, deduplicated and sorted, rejecting paths outside root.
func NormalizeConsumed(root string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(paths))
	var out []string
	for _, raw := range paths {
		if strings.TrimSpace(raw) == "" {
			return nil, errors.New("consumed path must not be blank")
		}
		abs := raw
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, filepath.FromSlash(raw))
		}
		rel, err := filepath.Rel(root, filepath.Clean(abs))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("consumed path %q is outside the repository root", raw)
		}
		rel = filepath.ToSlash(rel)
		if _, dup := seen[rel]; !dup {
			seen[rel] = struct{}{}
			out = append(out, rel)
		}
	}
	slices.Sort(out)
	return out, nil
}
