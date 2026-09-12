package review

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

const (
	inventoryDataIntegrity = "data-integrity"
	inventoryErrorHandling = "error-handling"
)

func inventoryLanes() []string {
	return []string{"cli-ux", "api-contracts", inventoryDataIntegrity, inventoryErrorHandling, "dependency-policy", "security", "lifecycle-concurrency", "performance", "test-risk", "coupling", "architecture"}
}

// Options controls deterministic, local report selection after all scan
// packets have been collected. Filters never alter the underlying scan data.
type Options struct {
	Top       int
	Focus     string
	Include   []string
	Exclude   []string
	Inventory string
	WhyTop    int
}

// Rationale explains one selected read-queue row without adding provider text.
type Rationale struct {
	Rank  int      `json:"rank"`
	Path  string   `json:"path"`
	Score int      `json:"score"`
	Lane  string   `json:"lane"`
	Why   []string `json:"why"`
}

// Validate rejects invalid local report controls before scanning begins.
func (o Options) Validate() error {
	if o.Top < 0 || o.Top > maxReadQueue {
		return fmt.Errorf("--top must be between 0 and %d", maxReadQueue)
	}
	if o.WhyTop < 0 || o.WhyTop > maxReadQueue {
		return fmt.Errorf("--why-top must be between 0 and %d", maxReadQueue)
	}
	if o.Inventory != "" && !slices.Contains(inventoryLanes(), o.Inventory) {
		return fmt.Errorf("unknown --inventory review lane %q", o.Inventory)
	}
	if o.Focus != "" {
		if _, err := regexp.Compile("(?i)" + o.Focus); err != nil {
			return fmt.Errorf("invalid --focus expression: %w", err)
		}
	}
	for _, pattern := range append(append([]string{}, o.Include...), o.Exclude...) {
		if _, err := path.Match(strings.ReplaceAll(pattern, "**", "*"), ""); err != nil {
			return fmt.Errorf("invalid path pattern %q: %w", pattern, err)
		}
	}
	return nil
}

// ApplyOptions filters, caps, reranks, and explains a composed report.
func ApplyOptions(report Report, options Options) (Report, error) {
	if err := options.Validate(); err != nil {
		return Report{}, err
	}
	var focus *regexp.Regexp
	if options.Focus != "" {
		focus = regexp.MustCompile("(?i)" + options.Focus)
	}
	items := make([]ReadItem, 0, len(report.ReadQueue))
	for _, item := range report.ReadQueue {
		if !matchesOptions(item, options, focus) {
			continue
		}
		items = append(items, item)
	}
	if options.Top > 0 && len(items) > options.Top {
		items = items[:options.Top]
	}
	for i := range items {
		items[i].Rank = i + 1
		items[i].EvidenceID = EvidenceIdentity(items[i])
		items[i].Lane = CullDispositions(items[i : i+1])[0].Lane
	}
	report.ReadQueue = items
	report.Rationale = rationale(items, options.WhyTop)
	if report.CullLedger != nil {
		report.CullLedger = BuildCullLedger(items)
	}
	return report, nil
}

func matchesOptions(item ReadItem, options Options, focus *regexp.Regexp) bool {
	if len(options.Include) > 0 && !matchesAnyGlob(options.Include, item.Path) {
		return false
	}
	if matchesAnyGlob(options.Exclude, item.Path) {
		return false
	}
	if options.Inventory != "" && !hasInventoryLane(item, options.Inventory) {
		return false
	}
	if focus == nil {
		return true
	}
	return focus.MatchString(strings.Join(append([]string{item.Path, item.Lane}, item.Why...), "\n"))
}

func hasInventoryLane(item ReadItem, lane string) bool {
	if slices.Contains(item.Why, "risk:"+lane) {
		return true
	}
	switch lane {
	case inventoryDataIntegrity:
		return isDataIntegrityInventoryItem(item)
	case inventoryErrorHandling:
		return isErrorHandlingInventoryItem(item)
	case "security":
		return slices.Contains(item.Why, "risk:security")
	case "performance":
		return slices.Contains(item.Why, "risk:best-practices")
	case "cli-ux":
		return strings.HasPrefix(item.Path, "cmd/") || strings.HasPrefix(item.Path, "internal/cli/")
	case "test-risk":
		return strings.HasSuffix(item.Path, "_test.go")
	}
	return false
}

// isDataIntegrityInventoryItem ports the source review-plan fallback for
// evidence that is structurally related to durable state, even when an older
// scan row lacks the explicit risk:data-integrity reason.
func isDataIntegrityInventoryItem(item ReadItem) bool {
	lower := strings.ToLower(strings.ReplaceAll(item.Path, "\\", "/"))
	for _, term := range []string{"database", "repository", "migration", "storage", "cache"} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

// isErrorHandlingInventoryItem ports the source review-plan fallback for
// process-boundary evidence and CLI/main entrypoints.
func isErrorHandlingInventoryItem(item ReadItem) bool {
	for _, reason := range item.Why {
		if strings.Contains(reason, "shell_boundary") || strings.Contains(reason, "process_exit") || strings.Contains(reason, "error_context_dropped") {
			return true
		}
	}
	lower := strings.ToLower(strings.ReplaceAll(item.Path, "\\", "/"))
	return strings.Contains(lower, "cli") || strings.Contains(lower, "main.go")
}

func matchesAnyGlob(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if doublestarMatch(strings.Split(pattern, "/"), strings.Split(value, "/")) {
			return true
		}
	}
	return false
}

func doublestarMatch(pattern, value []string) bool {
	if len(pattern) == 0 {
		return len(value) == 0
	}
	if pattern[0] == "**" {
		return doublestarMatch(pattern[1:], value) || len(value) > 0 && doublestarMatch(pattern, value[1:])
	}
	if len(value) == 0 {
		return false
	}
	matched, err := path.Match(pattern[0], value[0])
	return err == nil && matched && doublestarMatch(pattern[1:], value[1:])
}

func rationale(items []ReadItem, top int) []Rationale {
	if top == 0 || len(items) == 0 {
		return nil
	}
	if top > len(items) {
		top = len(items)
	}
	result := make([]Rationale, top)
	for i, item := range items[:top] {
		result[i] = Rationale{Rank: item.Rank, Path: item.Path, Score: item.Score, Lane: item.Lane, Why: item.Why}
	}
	return result
}
