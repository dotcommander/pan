package review

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/provider"
)

func verdictFixture(path, lane string, score, rank int) ReadItem {
	row := ReadItem{Path: path, Lane: lane, Score: score, Rank: rank, Why: []string{"risk:security", "risk:lifecycle"}}
	row.EvidenceID = EvidenceIdentity(row)
	return row
}

func TestModelKeepsDeterministicEvidenceAndCullSlots(t *testing.T) {
	t.Parallel()
	input := []ReadItem{verdictFixture("a.go", LaneKept, 90, 1), verdictFixture("a_test.go", LaneTest, 80, 2), verdictFixture("b.go", LaneKept, 70, 3), verdictFixture("c.go", LaneKept, 60, 4), verdictFixture("d.go", LaneKept, 50, 5)}
	report := Report{ReadQueue: input, Rationale: rationale(input, 3), CullLedger: BuildCullLedger(input)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.ResponseFormat != nil {
			t.Error("array response constrained as object")
		}
		if strings.Contains(request.Messages[0].Content, "a_test.go") {
			t.Error("non-kept row requested")
		}
		body := provider.Response{Model: "fixture", Choices: []provider.Choice{{Message: provider.Message{Role: "assistant", Content: `[{"index":0,"score":1},{"index":2,"score":5},{"index":3,"score":5}]`}}}}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)
	result, err := (ModelOptions{Model: "fixture", BaseURL: server.URL, NoCache: true}).Score(t.Context(), report)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"c.go", "a_test.go", "b.go", "d.go", "a.go"}
	byPath := map[string]ReadItem{}
	for _, row := range input {
		byPath[row.Path] = row
	}
	for i, row := range result.ReadQueue {
		if row.Path != want[i] || row.Rank != i+1 {
			t.Fatalf("row %d: %#v", i, row)
		}
		before := byPath[row.Path]
		if row.Score != before.Score || row.EvidenceID != before.EvidenceID || row.Lane != before.Lane || !reflect.DeepEqual(row.Why, before.Why) {
			t.Fatalf("deterministic evidence changed: %#v", row)
		}
		if result.CullLedger.Entries[i].Path != row.Path || result.CullLedger.Entries[i].Rank != row.Rank {
			t.Fatal("stale cull projection")
		}
	}
	if result.Rationale[0].Path != "c.go" || result.Rationale[2].Path != "b.go" {
		t.Fatal("stale rationale")
	}
	if report.ReadQueue[0].ModelVerdict != nil || report.ReadQueue[0].Path != "a.go" || report.CullLedger.Entries[0].Path != "a.go" {
		t.Fatal("input mutated")
	}
	if modelRankingStatus(result.ReadQueue) != "partial" {
		t.Fatal("partial ranking mislabeled")
	}
}

func TestCachedVerdictNeverChangesMissingPromptIndices(t *testing.T) {
	t.Parallel()
	rows := []ReadItem{verdictFixture("cached.go", LaneKept, 60, 1), verdictFixture("fresh.go", LaneKept, 50, 2)}
	original := cloneReadItem(rows[0])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.Request
		_ = json.NewDecoder(r.Body).Decode(&request)
		prompt := request.Messages[0].Content
		if strings.Contains(prompt, "cached.go") || !strings.Contains(prompt, `"index":0,"path":"fresh.go"`) {
			t.Errorf("incorrect request-local input: %s", prompt)
		}
		_ = json.NewEncoder(w).Encode(provider.Response{Model: "fixture", Choices: []provider.Choice{{Message: provider.Message{Content: `[{"index":0,"score":4}]`}}}})
	}))
	t.Cleanup(server.Close)
	options := ModelOptions{Model: "fixture", BaseURL: server.URL}
	client, err := provider.New(provider.Config{Model: "fixture", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	cache := modelScoreCache{entries: map[string]cachedModelScore{cacheKey(options, rows[0]): {Score: 5}}}
	if err := options.scoreBatch(t.Context(), client, rows, &cache); err != nil {
		t.Fatal(err)
	}
	if rows[0].Score != original.Score || !reflect.DeepEqual(rows[0].Why, original.Why) || rows[0].ModelVerdict.Score != 5 || rows[1].ModelVerdict.Score != 4 {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestStrictModelArrays(t *testing.T) {
	t.Parallel()
	for _, content := range []string{`null`, `{}`, "```json\n[]\n```", `[{"score":2}]`, `[{"index":0,"score":null}]`, `[{"index":0,"score":1.5}]`, `[{"index":0,"score":1,"extra":true}]`, `[] []`} {
		t.Run(content, func(t *testing.T) {
			t.Parallel()
			if _, err := parseModelScores(content); err == nil {
				t.Fatalf("accepted %s", content)
			}
		})
	}
}

func TestMalformedModelIndicesDegradeWithoutRecull(t *testing.T) {
	t.Parallel()
	for _, content := range []string{`[{"index":0,"score":5},{"index":0,"score":4}]`, `[{"index":2,"score":5}]`} {
		t.Run(content, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(provider.Response{Model: "fixture", Choices: []provider.Choice{{Message: provider.Message{Content: content}}}})
			}))
			t.Cleanup(server.Close)
			row := verdictFixture("a.go", LaneKept, 100, 1)
			result, err := (ModelOptions{Model: "fixture", BaseURL: server.URL, NoCache: true}).Score(t.Context(), Report{ReadQueue: []ReadItem{row}})
			if err != nil {
				t.Fatal(err)
			}
			got := result.ReadQueue[0]
			if got.ModelVerdict == nil || got.ModelVerdict.Status != "error" || got.Score != row.Score || got.EvidenceID != row.EvidenceID || got.Lane != row.Lane {
				t.Fatalf("unsafe fallback: %#v", got)
			}
			if modelRankingStatus(result.ReadQueue) != "unavailable" {
				t.Fatal("failure mislabeled")
			}
		})
	}
}

func TestModelDocumentV2RoundTripAndV1Compatibility(t *testing.T) {
	t.Parallel()
	row := verdictFixture("a.go", LaneKept, 100, 1)
	v1 := NewDocument(0, Report{ReadQueue: []ReadItem{row}})
	bytes1, err := v1.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseDocument(bytes1)
	if err != nil || decoded.Schema != DocumentSchema {
		t.Fatalf("v1: %v", err)
	}
	for _, verdict := range []*ModelVerdict{{Status: "success", Score: 4}, {Status: "inconclusive", Detail: "abstained"}, {Status: "error", Detail: "unavailable"}} {
		annotated := row
		annotated.ModelVerdict = verdict
		v2 := NewDocument(0, Report{ReadQueue: []ReadItem{annotated}})
		bytes2, err := v2.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := ParseDocument(bytes2)
		if err != nil || decoded.Schema != DocumentSchemaV2 || decoded.ReadQueue[0].EvidenceID != row.EvidenceID || decoded.ReportID == v1.ReportID {
			t.Fatalf("v2: %v %#v", err, decoded)
		}
		v2.Schema = DocumentSchema
		v2.ReportID = reportIdentity(v2)
		bad, _ := v2.Bytes()
		if _, err := ParseDocument(bad); err == nil {
			t.Fatal("v1 accepted annotations")
		}
	}
}

func TestCapturedHashesIgnoreLiveFiles(t *testing.T) {
	t.Parallel()
	// The hash owner reads only the captured map, including an explicitly empty file.
	hashes := CapturedContentHashes(analyze.Snapshot{Captured: map[string][]byte{"empty.go": {}}})
	if hashes["empty.go"] != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" || len(hashes) != 1 {
		t.Fatalf("hashes = %#v", hashes)
	}
}
