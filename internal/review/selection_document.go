package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/dotcommander/pan/internal/analyze"
)

// SelectionDocumentSchema identifies the deterministic selection preview.
const SelectionDocumentSchema = "pan.review-selection/v1"

const selectionPopulationScope = "composed_read_queue"

// SelectionPopulation names the bounded universe explained by a selection
// document. It is never a claim that the analyzer enumerated every file.
type SelectionPopulation struct {
	Scope         string `json:"scope"`
	UpstreamLimit int    `json:"upstream_limit"`
}

// SelectionOptions is the public projection of effective local selection
// controls. Provider and cull controls deliberately have no representation.
type SelectionOptions struct {
	Top       int      `json:"top"`
	Focus     string   `json:"focus"`
	Include   []string `json:"include"`
	Exclude   []string `json:"exclude"`
	Inventory string   `json:"inventory"`
	WhyTop    int      `json:"why_top"`
}

// SelectionAnalysis preserves bounded snapshot status without converting an
// omitted or skipped path into a per-item selection outcome.
type SelectionAnalysis struct {
	Complete     bool     `json:"complete"`
	Limits       []string `json:"limits"`
	Skipped      []string `json:"skipped"`
	SkippedCount int      `json:"skipped_count"`
}

// SelectionSummary reconciles every item in the declared population.
type SelectionSummary struct {
	Candidates    int `json:"candidates"`
	Selected      int `json:"selected"`
	Filtered      int `json:"filtered"`
	Deprioritized int `json:"deprioritized"`
}

// SelectionDocument explains the decisions made for one frozen composed read
// queue. It recommends reading order; it does not prove analysis completion.
type SelectionDocument struct {
	Schema      string              `json:"schema"`
	SelectionID string              `json:"selection_id"`
	Population  SelectionPopulation `json:"population"`
	Options     SelectionOptions    `json:"options"`
	Analysis    SelectionAnalysis   `json:"analysis"`
	Summary     SelectionSummary    `json:"summary"`
	Items       []SelectionItem     `json:"items"`
}

// NewSelectionDocument projects one selection pass into the versioned public
// document. The report must already contain the at-most-100 composed queue.
func NewSelectionDocument(status analyze.Status, report Report, options Options) (SelectionDocument, error) {
	if len(report.ReadQueue) > maxReadQueue {
		return SelectionDocument{}, fmt.Errorf("review: selection population exceeds %d rows", maxReadQueue)
	}
	result, err := ApplyOptionsWithSelection(report, options)
	if err != nil {
		return SelectionDocument{}, err
	}
	doc := SelectionDocument{
		Schema: SelectionDocumentSchema,
		Population: SelectionPopulation{
			Scope:         selectionPopulationScope,
			UpstreamLimit: maxReadQueue,
		},
		Options: SelectionOptions{
			Top:       options.Top,
			Focus:     options.Focus,
			Include:   normalizeStrings(options.Include),
			Exclude:   normalizeStrings(options.Exclude),
			Inventory: options.Inventory,
			WhyTop:    options.WhyTop,
		},
		Analysis: SelectionAnalysis{
			Complete:     status.Complete,
			Limits:       normalizeStrings(status.Limits),
			Skipped:      normalizeStrings(status.Skipped),
			SkippedCount: status.SkippedCount,
		},
		Items: normalizeSelectionItems(result.Items),
	}
	doc.Summary = selectionSummary(doc.Items)
	if err := validateSelectionContent(doc); err != nil {
		return SelectionDocument{}, err
	}
	doc.SelectionID = selectionIdentity(doc)
	return doc, nil
}

// Bytes renders the selection document as compact deterministic JSON with one
// trailing newline.
func (d SelectionDocument) Bytes() ([]byte, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("review: encode selection document: %w", err)
	}
	return append(data, '\n'), nil
}

// ParseSelectionDocument decodes and validates selection document bytes. A
// valid identity alone is insufficient: decisions are recomputed by the same
// selection owner that produces deterministic reports.
func ParseSelectionDocument(data []byte) (SelectionDocument, error) {
	if len(data) > maxDocumentBytes {
		return SelectionDocument{}, fmt.Errorf("selection document exceeds %d bytes", maxDocumentBytes)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return SelectionDocument{}, errors.New("selection document is not a JSON object")
	}
	if err := requireSelectionFields(fields); err != nil {
		return SelectionDocument{}, err
	}
	doc, err := decodeSelectionDocument(data)
	if err != nil {
		return SelectionDocument{}, err
	}
	if err := validateSelectionContent(doc); err != nil {
		return SelectionDocument{}, err
	}
	if selectionIdentity(doc) != doc.SelectionID {
		return SelectionDocument{}, errors.New("selection identity mismatch")
	}
	return doc, nil
}

func normalizeStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return slices.Clone(values)
}

func normalizeSelectionItems(items []SelectionItem) []SelectionItem {
	result := make([]SelectionItem, len(items))
	for i, item := range items {
		item.Input = cloneReadItem(item.Input)
		if item.Input.Why == nil {
			item.Input.Why = []string{}
		}
		result[i] = item
	}
	return result
}

func selectionSummary(items []SelectionItem) SelectionSummary {
	summary := SelectionSummary{Candidates: len(items)}
	for _, item := range items {
		switch item.Decision {
		case DecisionSelected:
			summary.Selected++
		case DecisionFiltered:
			summary.Filtered++
		case DecisionDeprioritized:
			summary.Deprioritized++
		}
	}
	return summary
}

func selectionIdentity(doc SelectionDocument) string {
	payload := struct {
		Schema     string              `json:"schema"`
		Population SelectionPopulation `json:"population"`
		Options    SelectionOptions    `json:"options"`
		Analysis   SelectionAnalysis   `json:"analysis"`
		Summary    SelectionSummary    `json:"summary"`
		Items      []SelectionItem     `json:"items"`
	}{doc.Schema, doc.Population, doc.Options, doc.Analysis, doc.Summary, doc.Items}
	return sha256Identity(SelectionDocumentSchema, payload)
}

func requireSelectionFields(fields map[string]json.RawMessage) error {
	if err := requireJSONFields(fields, "schema", "selection_id", "population", "options", "analysis", "summary", "items"); err != nil {
		return fmt.Errorf("selection document: %w", err)
	}
	nested := []struct {
		name   string
		raw    json.RawMessage
		fields []string
	}{
		{"population", fields["population"], []string{"scope", "upstream_limit"}},
		{"options", fields["options"], []string{"top", "focus", "include", "exclude", "inventory", "why_top"}},
		{"analysis", fields["analysis"], []string{"complete", "limits", "skipped", "skipped_count"}},
		{"summary", fields["summary"], []string{"candidates", "selected", "filtered", "deprioritized"}},
	}
	for _, section := range nested {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(section.raw, &object); err != nil || object == nil {
			return fmt.Errorf("selection document %s is not an object", section.name)
		}
		if err := requireJSONFields(object, section.fields...); err != nil {
			return fmt.Errorf("selection document %s: %w", section.name, err)
		}
	}
	for i, raw := range selectionItemRawList(fields["items"]) {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return fmt.Errorf("selection document item %d is not an object", i+1)
		}
		if err := requireJSONFields(object, "input", "decision", "reason_code", "selected_rank"); err != nil {
			return fmt.Errorf("selection document item %d: %w", i+1, err)
		}
		var input map[string]json.RawMessage
		if err := json.Unmarshal(object["input"], &input); err != nil || input == nil {
			return fmt.Errorf("selection document item %d input is not an object", i+1)
		}
		if err := requireJSONFields(input, "rank", "evidence_id", "path", "score", "lane", "why"); err != nil {
			return fmt.Errorf("selection document item %d input: %w", i+1, err)
		}
	}
	return nil
}

func selectionItemRawList(raw json.RawMessage) []json.RawMessage {
	var items []json.RawMessage
	// The caller has already established that raw is present. An invalid or
	// null array is reported by strict decoding after field presence checks.
	_ = json.Unmarshal(raw, &items)
	return items
}

func requireJSONFields(fields map[string]json.RawMessage, names ...string) error {
	for _, name := range names {
		raw, ok := fields[name]
		if !ok {
			return fmt.Errorf("is missing %s", name)
		}
		if bytes.Equal(raw, []byte("null")) {
			return fmt.Errorf("%s must not be null", name)
		}
	}
	return nil
}

func decodeSelectionDocument(data []byte) (SelectionDocument, error) {
	var doc SelectionDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return SelectionDocument{}, fmt.Errorf("decode selection document: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SelectionDocument{}, errors.New("selection document has trailing data")
	}
	doc.Options.Include = normalizeStrings(doc.Options.Include)
	doc.Options.Exclude = normalizeStrings(doc.Options.Exclude)
	doc.Analysis.Limits = normalizeStrings(doc.Analysis.Limits)
	doc.Analysis.Skipped = normalizeStrings(doc.Analysis.Skipped)
	doc.Items = normalizeSelectionItems(doc.Items)
	return doc, nil
}

func validateSelectionContent(doc SelectionDocument) error {
	if doc.Schema != SelectionDocumentSchema {
		return fmt.Errorf("unsupported selection schema %q", doc.Schema)
	}
	if doc.Population.Scope != selectionPopulationScope || doc.Population.UpstreamLimit != maxReadQueue {
		return fmt.Errorf("unsupported selection population %#v", doc.Population)
	}
	if len(doc.Items) > maxReadQueue {
		return fmt.Errorf("selection population exceeds %d rows", maxReadQueue)
	}
	if doc.Analysis.SkippedCount < 0 || doc.Analysis.SkippedCount < len(doc.Analysis.Skipped) {
		return errors.New("selection analysis skipped count is inconsistent")
	}
	options := reviewOptions(doc.Options)
	if err := options.Validate(); err != nil {
		return err
	}
	if err := validateSelectionItems(doc, options); err != nil {
		return err
	}
	if doc.Summary != selectionSummary(doc.Items) {
		return errors.New("selection summary does not match items")
	}
	return nil
}

func validateSelectionItems(doc SelectionDocument, options Options) error {
	validator := selectionItemValidator{seenPaths: make(map[string]bool, len(doc.Items))}
	for i, item := range doc.Items {
		if err := validator.validate(item, i+1); err != nil {
			return err
		}
	}
	return compareRecomputedSelection(doc, options)
}

type selectionItemValidator struct {
	seenPaths    map[string]bool
	selectedRank int
}

func (v *selectionItemValidator) validate(item SelectionItem, position int) error {
	if err := v.validateInput(item.Input, position); err != nil {
		return err
	}
	if !validDecisionPair(item.Decision, item.ReasonCode) {
		return fmt.Errorf("selection item %d has invalid decision/reason pair %q/%q", position, item.Decision, item.ReasonCode)
	}
	return v.validateSelectedRank(item, position)
}

func (v *selectionItemValidator) validateInput(input ReadItem, position int) error {
	if input.Rank != position {
		return fmt.Errorf("selection item %d has input rank %d", position, input.Rank)
	}
	if input.Path == "" {
		return fmt.Errorf("selection item %d has an empty path", position)
	}
	if v.seenPaths[input.Path] {
		return fmt.Errorf("selection item %d duplicates path %q", position, input.Path)
	}
	v.seenPaths[input.Path] = true
	if !ValidIdentity(input.EvidenceID) {
		return fmt.Errorf("selection item %d has a malformed evidence identity", position)
	}
	if input.Score < 0 {
		return fmt.Errorf("selection item %d has a negative score", position)
	}
	if !ValidLane(input.Lane) {
		return fmt.Errorf("selection item %d has non-canonical lane %q", position, input.Lane)
	}
	return nil
}

func (v *selectionItemValidator) validateSelectedRank(item SelectionItem, position int) error {
	if item.Decision != DecisionSelected {
		if item.SelectedRank != 0 {
			return fmt.Errorf("selection item %d has nonselected rank %d", position, item.SelectedRank)
		}
		return nil
	}
	v.selectedRank++
	if item.SelectedRank != v.selectedRank {
		return fmt.Errorf("selection item %d has selected rank %d", position, item.SelectedRank)
	}
	return nil
}

func validDecisionPair(decision SelectionDecision, reason SelectionReason) bool {
	switch {
	case decision == DecisionSelected && reason == ReasonSelected:
		return true
	case decision == DecisionFiltered && slices.Contains([]SelectionReason{
		ReasonIncludeMismatch, ReasonExcludeMatch, ReasonInventoryMismatch, ReasonFocusMismatch,
	}, reason):
		return true
	case decision == DecisionDeprioritized && reason == ReasonTopLimit:
		return true
	default:
		return false
	}
}

func compareRecomputedSelection(doc SelectionDocument, options Options) error {
	inputs := make([]ReadItem, len(doc.Items))
	for i, item := range doc.Items {
		inputs[i] = cloneReadItem(item.Input)
	}
	expected, err := ApplyOptionsWithSelection(Report{ReadQueue: inputs}, options)
	if err != nil {
		return err
	}
	if len(expected.Items) != len(doc.Items) {
		return errors.New("selection decision count mismatch")
	}
	for i, want := range expected.Items {
		got := doc.Items[i]
		if got.Decision != want.Decision || got.ReasonCode != want.ReasonCode || got.SelectedRank != want.SelectedRank {
			return fmt.Errorf("selection item %d decision mismatch", i+1)
		}
		if !equalReadInput(got.Input, want.Input) {
			return fmt.Errorf("selection item %d input mismatch", i+1)
		}
	}
	selected := 0
	for _, item := range doc.Items {
		if item.Decision != DecisionSelected {
			continue
		}
		if selected >= len(expected.Report.ReadQueue) {
			return errors.New("selection has more selected rows than report queue")
		}
		projected := cloneReadItem(item.Input)
		projected.Rank = item.SelectedRank
		projected.EvidenceID = EvidenceIdentity(projected)
		projected.Lane = CullDispositions([]ReadItem{projected})[0].Lane
		if !equalReadInput(projected, expected.Report.ReadQueue[selected]) {
			return fmt.Errorf("selected rank %d does not match report queue", item.SelectedRank)
		}
		selected++
	}
	if selected != len(expected.Report.ReadQueue) {
		return errors.New("selection report queue mismatch")
	}
	return nil
}

func reviewOptions(options SelectionOptions) Options {
	return Options(options)
}

func equalReadInput(a, b ReadItem) bool {
	return a.Rank == b.Rank && a.EvidenceID == b.EvidenceID && a.Path == b.Path &&
		a.Score == b.Score && a.Lane == b.Lane && slices.Equal(a.Why, b.Why)
}
