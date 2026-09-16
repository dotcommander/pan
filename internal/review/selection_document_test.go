package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
)

func TestSelectionDocumentProjectsFrozenSelection(t *testing.T) {
	t.Parallel()
	status, report, options := selectionFixture()
	document, err := NewSelectionDocument(status, report, options)
	if err != nil {
		t.Fatal(err)
	}
	assertSelectionDocumentMetadata(t, document)
	assertSelectionDocumentItems(t, document)
	assertSelectionDocumentLegacyProjection(t, document, report, options)
}

type expectedSelectionItem struct {
	path         string
	lane         string
	decision     SelectionDecision
	reason       SelectionReason
	selectedRank int
}

func assertSelectionDocumentMetadata(t *testing.T, document SelectionDocument) {
	t.Helper()
	if document.Schema != SelectionDocumentSchema || document.Population.Scope != "composed_read_queue" || document.Population.UpstreamLimit != 100 {
		t.Fatalf("population = %#v", document.Population)
	}
	want := SelectionAnalysis{Complete: false, Limits: []string{"max_files:2"}, Skipped: []string{"vendor/legacy"}, SkippedCount: 3}
	if !reflect.DeepEqual(document.Analysis, want) {
		t.Fatalf("analysis = %#v", document.Analysis)
	}
}

func assertSelectionDocumentItems(t *testing.T, document SelectionDocument) {
	t.Helper()
	want := []expectedSelectionItem{
		{"internal/auth/token.go", LaneKept, DecisionSelected, ReasonSelected, 1},
		{"internal/auth/token_test.go", LaneTest, DecisionDeprioritized, ReasonTopLimit, 0},
		{"docs/guide.md", LaneDocs, DecisionFiltered, ReasonIncludeMismatch, 0},
	}
	if len(document.Items) != len(want) || document.Summary != (SelectionSummary{Candidates: 3, Selected: 1, Filtered: 1, Deprioritized: 1}) {
		t.Fatalf("items=%d summary=%#v", len(document.Items), document.Summary)
	}
	for i, expected := range want {
		assertSelectionDocumentItem(t, document.Items[i], i+1, expected)
	}
}

func assertSelectionDocumentItem(t *testing.T, item SelectionItem, rank int, expected expectedSelectionItem) {
	t.Helper()
	if item.Input.Path != expected.path || item.Input.Lane != expected.lane || item.Input.Rank != rank ||
		item.Decision != expected.decision || item.ReasonCode != expected.reason || item.SelectedRank != expected.selectedRank {
		t.Fatalf("item %d = %#v, want %+v", rank-1, item, expected)
	}
}

func assertSelectionDocumentLegacyProjection(t *testing.T, document SelectionDocument, report Report, options Options) {
	t.Helper()
	legacy, err := ApplyOptions(report, options)
	if err != nil {
		t.Fatal(err)
	}
	selected := 0
	for _, item := range document.Items {
		if item.Decision != DecisionSelected {
			continue
		}
		assertSelectedProjection(t, item, legacy.ReadQueue[selected])
		selected++
	}
	if selected != len(legacy.ReadQueue) {
		t.Fatalf("selected=%d report rows=%d", selected, len(legacy.ReadQueue))
	}
}

func assertSelectedProjection(t *testing.T, item SelectionItem, row ReadItem) {
	t.Helper()
	if item.SelectedRank != row.Rank || item.Input.Path != row.Path || item.Input.Score != row.Score ||
		item.Input.Lane != row.Lane || !slices.Equal(item.Input.Why, row.Why) {
		t.Fatalf("selected projection %#v does not match report row %#v", item, row)
	}
}

func TestSelectionDocumentCanonicalizesEmptyPopulation(t *testing.T) {
	t.Parallel()
	document, err := NewSelectionDocument(analyze.Status{}, Report{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	body, err := document.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`{"schema":%q,"selection_id":%q,"population":{"scope":"composed_read_queue","upstream_limit":100},"options":{"top":0,"focus":"","include":[],"exclude":[],"inventory":"","why_top":0},"analysis":{"complete":false,"limits":[],"skipped":[],"skipped_count":0},"summary":{"candidates":0,"selected":0,"filtered":0,"deprioritized":0},"items":[]}`+"\n",
		SelectionDocumentSchema, document.SelectionID)
	if !bytes.Equal(body, []byte(want)) {
		t.Fatalf("canonical bytes:\n got %s\nwant %s", body, want)
	}
	again, err := document.Bytes()
	if err != nil || !bytes.Equal(body, again) {
		t.Fatalf("repeat bytes=%s error=%v", again, err)
	}
	parsed, err := ParseSelectionDocument(body)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed, document) {
		t.Fatalf("parsed = %#v, want %#v", parsed, document)
	}
}

func TestSelectionDocumentIdentityCoversInputsOptionsAndStatus(t *testing.T) {
	t.Parallel()
	status, report, options := selectionFixture()
	base, err := NewSelectionDocument(status, report, options)
	if err != nil {
		t.Fatal(err)
	}
	changedRow := selectionDocumentForPath(t, "internal/auth/other.go")
	changedOptions, err := NewSelectionDocument(status, report, Options{Top: 2, Include: options.Include})
	if err != nil {
		t.Fatal(err)
	}
	changedStatus := status
	changedStatus.Complete = true
	changedAnalysis, err := NewSelectionDocument(changedStatus, report, options)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{base.SelectionID, changedRow.SelectionID, changedOptions.SelectionID, changedAnalysis.SelectionID}
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[0] {
			t.Fatalf("identity %d did not change: %s", i, ids[i])
		}
	}
	if !ValidIdentity(base.SelectionID) {
		t.Fatalf("identity = %q", base.SelectionID)
	}
}

func TestParseSelectionDocumentRejectsInvalidContracts(t *testing.T) {
	t.Parallel()
	status, report, options := selectionFixture()
	valid, err := NewSelectionDocument(status, report, options)
	if err != nil {
		t.Fatal(err)
	}
	mutated := func(edit func(*SelectionDocument)) []byte {
		t.Helper()
		document := valid
		document.Options.Include = slices.Clone(valid.Options.Include)
		document.Options.Exclude = slices.Clone(valid.Options.Exclude)
		document.Analysis.Limits = slices.Clone(valid.Analysis.Limits)
		document.Analysis.Skipped = slices.Clone(valid.Analysis.Skipped)
		document.Items = make([]SelectionItem, len(valid.Items))
		for i := range valid.Items {
			document.Items[i] = valid.Items[i]
			document.Items[i].Input.Why = slices.Clone(valid.Items[i].Input.Why)
		}
		edit(&document)
		if document.SelectionID != "" && document.SelectionID != "invalid" {
			document.SelectionID = selectionIdentity(document)
		}
		data, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	raw := func(edit func(map[string]any)) []byte {
		t.Helper()
		data, err := valid.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if unmarshalErr := json.Unmarshal(data, &object); unmarshalErr != nil {
			t.Fatal(unmarshalErr)
		}
		edit(object)
		data, err = json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	tooMany := make([]SelectionItem, maxReadQueue+1)
	for i := range tooMany {
		tooMany[i] = SelectionItem{Input: selectionFixtureItem(i+1, fmt.Sprintf("pkg/file-%03d.go", i), 0), Decision: DecisionSelected, ReasonCode: ReasonSelected, SelectedRank: i + 1}
	}
	tests := []struct {
		name string
		data []byte
	}{
		{"unsupported schema", mutated(func(document *SelectionDocument) { document.Schema = "pan.wrong/v1" })},
		{"wrong population", mutated(func(document *SelectionDocument) { document.Population.Scope = "repository" })},
		{"duplicate path", mutated(func(document *SelectionDocument) { document.Items[2].Input.Path = document.Items[0].Input.Path })},
		{"invalid decision pair", mutated(func(document *SelectionDocument) { document.Items[0].ReasonCode = ReasonIncludeMismatch })},
		{"unknown decision", mutated(func(document *SelectionDocument) { document.Items[0].Decision = "analyzed" })},
		{"noncontiguous selected rank", mutated(func(document *SelectionDocument) { document.Items[0].SelectedRank = 2 })},
		{"mismatched summary", mutated(func(document *SelectionDocument) { document.Summary.Selected = 2 })},
		{"malformed evidence identity", mutated(func(document *SelectionDocument) { document.Items[0].Input.EvidenceID = "not-an-identity" })},
		{"invalid lane", mutated(func(document *SelectionDocument) { document.Items[0].Input.Lane = "cull" })},
		{"negative score", mutated(func(document *SelectionDocument) { document.Items[0].Input.Score = -1 })},
		{"more than 100 items", mutated(func(document *SelectionDocument) {
			document.Items = tooMany
			document.Summary = selectionSummary(tooMany)
		})},
		{"mismatched identity", raw(func(object map[string]any) { object["selection_id"] = "sha256:" + strings.Repeat("b", 64) })},
		{"missing field", raw(func(object map[string]any) { delete(object, "selection_id") })},
		{"unknown field", raw(func(object map[string]any) { object["coverage"] = true })},
		{"null collection", raw(func(object map[string]any) {
			options := object["options"].(map[string]any)
			options["include"] = nil
		})},
		{"trailing object", append(raw(func(object map[string]any) {}), []byte(`{"extra":true}`)...)},
		{"oversized", bytes.Repeat([]byte(" "), maxDocumentBytes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseSelectionDocument(tt.data); err == nil {
				t.Fatalf("accepted invalid document: %s", tt.data)
			}
		})
	}
}

func selectionFixture() (analyze.Status, Report, Options) {
	status := analyze.Status{Complete: false, Limits: []string{"max_files:2"}, Skipped: []string{"vendor/legacy"}, SkippedCount: 3}
	report := Report{ReadQueue: []ReadItem{
		selectionFixtureItem(1, "internal/auth/token.go", 30, "risk:security", "churn:2 commits"),
		selectionFixtureItem(2, "internal/auth/token_test.go", 25, "risk:security", "churn:2 commits"),
		selectionFixtureItem(3, "docs/guide.md", 20, "risk:docs", "churn:2 commits"),
	}}
	return status, report, Options{Top: 1, Include: []string{"internal/**"}}
}

func selectionFixtureItem(rank int, path string, score int, why ...string) ReadItem {
	item := ReadItem{Rank: rank, Path: path, Score: score, Why: why}
	item.EvidenceID = EvidenceIdentity(item)
	item.Lane = CullDispositions([]ReadItem{item})[0].Lane
	return item
}

func selectionDocumentForPath(t *testing.T, path string) SelectionDocument {
	t.Helper()
	document, err := NewSelectionDocument(analyze.Status{}, Report{ReadQueue: []ReadItem{selectionFixtureItem(1, path, 0)}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return document
}
