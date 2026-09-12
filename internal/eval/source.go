package eval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dotcommander/pan/internal/review"
)

const (
	sourceReportSchema      = "slither.report/v1"
	sourceLaneKept          = "kept_for_premium"
	sourceLaneAlternates    = "alternates"
	sourceLaneGenerated     = "culled_generated_or_report"
	sourceLaneDocs          = "culled_documentation"
	sourceLaneTest          = "culled_test_only"
	sourceLaneLowSignal     = "culled_low_signal"
	sourceLaneDuplicate     = "culled_duplicate_surface"
	sourceLaneNeedsEvidence = "needs_more_evidence"
)

type evalReport struct {
	ID            string
	OutcomeSchema string
	Rows          []evalRow
}

type evalRow struct {
	EvidenceID string
	Score      int
	Lane       string
	Rank       int
}

type sourceState struct {
	Kind       string `json:"kind"`
	Head       string `json:"head"`
	Dirty      bool   `json:"dirty"`
	TreeDigest string `json:"tree_digest"`
}

type sourceParameters struct {
	Days            int      `json:"days"`
	MaxBytes        int64    `json:"max_bytes"`
	Top             int      `json:"top"`
	Focus           string   `json:"focus,omitempty"`
	Include         []string `json:"include,omitempty"`
	Exclude         []string `json:"exclude,omitempty"`
	Inventory       string   `json:"inventory,omitempty"`
	PatternsID      string   `json:"patterns_id"`
	Model           string   `json:"model,omitempty"`
	BaseURL         string   `json:"base_url,omitempty"`
	FallbackModels  []string `json:"fallback_models,omitempty"`
	ModelContractID string   `json:"model_contract_id,omitempty"`
}

type sourceProvenance struct {
	Deterministic int    `json:"deterministic"`
	Model         *int   `json:"model,omitempty"`
	SelectedBy    string `json:"selected_by"`
}

type sourceRow struct {
	EvidenceID             string           `json:"evidence_id"`
	Score                  int              `json:"score"`
	ScoreProvenance        sourceProvenance `json:"score_provenance"`
	Path                   string           `json:"path"`
	Confidence             string           `json:"confidence"`
	SeedScore              float64          `json:"seed_score"`
	PathRisk               int              `json:"path_risk"`
	ContentRisk            int              `json:"content_risk"`
	WorkflowSecurityRisk   int              `json:"workflow_security_risk"`
	MigrationSafetyRisk    int              `json:"migration_safety_risk"`
	ContainerBuildRisk     int              `json:"container_build_risk"`
	KubernetesSecurityRisk int              `json:"kubernetes_security_risk"`
	TerraformSecurityRisk  int              `json:"terraform_security_risk"`
	OpenAPIContractRisk    int              `json:"openapi_contract_risk"`
	CORSSecurityRisk       int              `json:"cors_security_risk"`
	CookieSecurityRisk     int              `json:"cookie_security_risk"`
	DependencyHealthRisk   int              `json:"dependency_health_risk"`
	FlakeRisk              int              `json:"flake_risk"`
	OracleRisk             int              `json:"oracle_risk"`
	StaleMarkerRisk        int              `json:"stale_marker_risk"`
	EvidenceLayers         []string         `json:"evidence_layers"`
	Reasons                []string         `json:"reasons"`
}

type sourceEnvelope struct {
	SchemaVersion string           `json:"schema_version"`
	ReportID      string           `json:"report_id"`
	SourceState   sourceState      `json:"source_state"`
	Parameters    sourceParameters `json:"parameters"`
	Rows          json.RawMessage  `json:"rows"`
}

func readEvalReport(path string) (evalReport, error) {
	data, err := readBoundedReport(path)
	if err != nil {
		return evalReport{}, err
	}
	var schema struct {
		Schema        string `json:"schema"`
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		return evalReport{}, fmt.Errorf("decode report %s: %w", filepath.Base(path), err)
	}
	switch {
	case schema.Schema == review.DocumentSchema:
		doc, err := review.ParseDocument(data)
		if err != nil {
			return evalReport{}, fmt.Errorf("invalid report %s: %w", filepath.Base(path), err)
		}
		rows := make([]evalRow, len(doc.ReadQueue))
		for i, row := range doc.ReadQueue {
			rows[i] = evalRow{EvidenceID: row.EvidenceID, Score: row.Score, Lane: row.Lane, Rank: row.Rank}
		}
		return evalReport{ID: doc.ReportID, OutcomeSchema: OutcomeSchema, Rows: rows}, nil
	case schema.SchemaVersion == sourceReportSchema:
		return parseSourceReport(data, filepath.Base(path))
	default:
		return evalReport{}, fmt.Errorf("unsupported report schema in %s", filepath.Base(path))
	}
}

func readBoundedReport(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open report %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = file.Close() }()
	const maxReportBytes = 4 << 20
	data, err := io.ReadAll(io.LimitReader(file, maxReportBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read report %s: %w", filepath.Base(path), err)
	}
	if len(data) > maxReportBytes {
		return nil, fmt.Errorf("report %s exceeds %d bytes", filepath.Base(path), maxReportBytes)
	}
	return data, nil
}

func parseSourceReport(data []byte, name string) (evalReport, error) {
	envelope, rows, err := decodeSourceReport(data, name)
	if err != nil {
		return evalReport{}, err
	}
	identityRows, result, err := sourceEvaluationRows(rows, envelope.ReportID, name)
	if err != nil {
		return evalReport{}, err
	}
	identity := sourceReportIdentity(envelope.SchemaVersion, envelope.SourceState, envelope.Parameters, identityRows)
	if identity != envelope.ReportID {
		return evalReport{}, fmt.Errorf("report identity mismatch in %s", name)
	}
	for i, lane := range sourceLanes(rows) {
		result.Rows[i].Lane = lane
	}
	return result, nil
}

func decodeSourceReport(data []byte, name string) (sourceEnvelope, []sourceRow, error) {
	var envelope sourceEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&envelope); err != nil {
		return sourceEnvelope{}, nil, fmt.Errorf("decode report %s: %w", name, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return sourceEnvelope{}, nil, fmt.Errorf("decode report %s: trailing data", name)
	}
	if envelope.SchemaVersion != sourceReportSchema || !review.ValidIdentity(envelope.ReportID) || len(envelope.Rows) == 0 || string(envelope.Rows) == "null" {
		return sourceEnvelope{}, nil, fmt.Errorf("invalid report %s", name)
	}
	var rows []sourceRow
	if err := json.Unmarshal(envelope.Rows, &rows); err != nil || rows == nil {
		return sourceEnvelope{}, nil, fmt.Errorf("invalid report rows in %s", name)
	}
	return envelope, rows, nil
}

func sourceEvaluationRows(rows []sourceRow, reportID, name string) ([]struct {
	EvidenceID string `json:"evidence_id"`
	Score      int    `json:"score"`
}, evalReport, error) {
	identityRows := make([]struct {
		EvidenceID string `json:"evidence_id"`
		Score      int    `json:"score"`
	}, len(rows))
	result := evalReport{ID: reportID, OutcomeSchema: sourceOutcomeSchema, Rows: make([]evalRow, len(rows))}
	seen := make(map[string]bool, len(rows))
	for i, row := range rows {
		if !review.ValidIdentity(row.EvidenceID) || row.Score < 1 || row.Score > 5 || seen[row.EvidenceID] || !validSourceProvenance(row) {
			return nil, evalReport{}, fmt.Errorf("invalid report row %d in %s", i+1, name)
		}
		seen[row.EvidenceID] = true
		identityRows[i].EvidenceID, identityRows[i].Score = row.EvidenceID, row.Score
		result.Rows[i] = evalRow{EvidenceID: row.EvidenceID, Score: row.Score, Rank: i + 1}
	}
	return identityRows, result, nil
}

func validSourceProvenance(row sourceRow) bool {
	if row.ScoreProvenance.Deterministic < 1 || row.ScoreProvenance.Deterministic > 5 {
		return false
	}
	switch row.ScoreProvenance.SelectedBy {
	case "deterministic":
		return row.ScoreProvenance.Model == nil && row.Score == row.ScoreProvenance.Deterministic
	case "model":
		return row.ScoreProvenance.Model != nil && row.Score == *row.ScoreProvenance.Model
	default:
		return false
	}
}

func sourceReportIdentity(schema string, state sourceState, parameters sourceParameters, rows []struct {
	EvidenceID string `json:"evidence_id"`
	Score      int    `json:"score"`
}) string {
	payload, err := json.Marshal(struct {
		SchemaVersion string           `json:"schema_version"`
		SourceState   sourceState      `json:"source_state"`
		Parameters    sourceParameters `json:"parameters"`
		Rows          []struct {
			EvidenceID string `json:"evidence_id"`
			Score      int    `json:"score"`
		} `json:"rows"`
	}{schema, state, parameters, rows})
	if err != nil {
		panic(fmt.Sprintf("marshal source report identity: %v", err))
	}
	hash := sha256.New()
	hash.Write([]byte(sourceReportSchema))
	hash.Write([]byte{0})
	hash.Write(payload)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func validSourceLane(lane string) bool {
	switch lane {
	case sourceLaneKept, sourceLaneAlternates, sourceLaneGenerated, sourceLaneDocs, sourceLaneTest, sourceLaneLowSignal, sourceLaneDuplicate, sourceLaneNeedsEvidence:
		return true
	default:
		return false
	}
}

func sourceLanes(rows []sourceRow) []string {
	lanes := make([]string, len(rows))
	seen := map[string]string{}
	var premium []int
	for i, row := range rows {
		key := sourceSurfaceKey(row)
		_, duplicate := seen[key]
		switch {
		case sourceGenerated(row.Path):
			lanes[i] = sourceLaneGenerated
		case sourceTest(row.Path):
			lanes[i] = sourceLaneTest
		case sourceDocumentation(row.Path):
			lanes[i] = sourceLaneDocs
		case duplicate && row.Score < 4:
			lanes[i] = sourceLaneDuplicate
		case sourceKeepForPremium(row):
			premium = append(premium, i)
			seen[key] = row.Path
		case sourceNeedsMoreEvidence(row):
			lanes[i] = sourceLaneNeedsEvidence
		case row.Score >= 3:
			lanes[i] = sourceLaneAlternates
			seen[key] = row.Path
		default:
			lanes[i] = sourceLaneLowSignal
		}
	}
	sort.SliceStable(premium, func(i, j int) bool { return sourcePremiumLess(rows[premium[i]], rows[premium[j]]) })
	for i, index := range premium {
		if i < 24 {
			lanes[index] = sourceLaneKept
		} else {
			lanes[index] = sourceLaneAlternates
		}
	}
	return lanes
}

func sourceKeepForPremium(row sourceRow) bool {
	return row.Score >= 4 && (sourceEvidenceIntersections(row) >= 2 || sourceHighRisk(row))
}

func sourcePremiumLess(a, b sourceRow) bool {
	confidence := func(value string) int {
		switch value {
		case "high":
			return 3
		case "medium":
			return 2
		case "low":
			return 1
		}
		return 0
	}
	for _, pair := range [][2]float64{{float64(confidence(a.Confidence)), float64(confidence(b.Confidence))}, {float64(a.Score), float64(b.Score)}, {a.SeedScore, b.SeedScore}, {float64(sourceEvidenceIntersections(a)), float64(sourceEvidenceIntersections(b))}, {boolRank(sourceHighRisk(a)), boolRank(sourceHighRisk(b))}} {
		if pair[0] != pair[1] {
			return pair[0] > pair[1]
		}
	}
	return a.Path < b.Path
}

func boolRank(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func sourceHighRisk(row sourceRow) bool {
	return row.WorkflowSecurityRisk > 0 || row.MigrationSafetyRisk > 0 || row.ContainerBuildRisk > 0 || row.KubernetesSecurityRisk > 0 || row.TerraformSecurityRisk > 0 || row.OpenAPIContractRisk > 0 || row.CORSSecurityRisk > 0 || row.CookieSecurityRisk > 0 || row.DependencyHealthRisk > 0 || row.FlakeRisk > 0 || row.OracleRisk > 0 || row.StaleMarkerRisk > 0
}

func sourceGenerated(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	return containsAny(lower, []string{"generated", "/gen/", ".gen.", ".generated.", "/web-cache-", "pan-cull", "pan-report", "triage-report", "/reports/"}) || hasAnySuffix(lower, []string{".pb.go", ".min.js", ".bundle.js", ".gitignore", "/triage_patterns.json"}) || lower == "triage_patterns.json" || hasAnyPrefix(lower, []string{".work/", "prototypes/", "stubs/"}) || sourceCachedData(lower) || sourceGeneratedDocs(lower)
}

func containsAny(value string, parts []string) bool {
	for _, part := range parts {
		if strings.Contains(value, part) {
			return true
		}
	}
	return false
}
func hasAnySuffix(value string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}
func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
func sourceCachedData(path string) bool {
	return strings.HasPrefix(path, "data/") && strings.HasSuffix(path, ".html") && strings.Contains(path, "/cache")
}
func sourceGeneratedDocs(path string) bool {
	return strings.HasPrefix(path, "docs/") && (strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".json") || containsAny(path, []string{"report", "scoreboard", "benchmark"}))
}

func sourceTest(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	return strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") || strings.Contains(lower, "/fixtures/") || strings.Contains(lower, "/testdata/") || strings.Contains(lower, "testdata") || strings.Contains(lower, "fixture") || strings.HasPrefix(lower, "test/") || strings.HasPrefix(lower, "tests/") || strings.HasPrefix(lower, "fixtures/") || strings.HasPrefix(lower, "testdata/") || strings.HasSuffix(lower, "_test.go") || strings.HasSuffix(lower, ".test.ts") || strings.HasSuffix(lower, ".test.js") || strings.HasSuffix(lower, ".spec.ts") || strings.HasSuffix(lower, ".spec.js")
}

func sourceDocumentation(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	return strings.HasPrefix(lower, "docs/") || strings.HasPrefix(lower, "doc/") || strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".mdx") || strings.HasSuffix(lower, ".rst")
}

func sourceSurfaceKey(row sourceRow) string {
	path := filepath.ToSlash(row.Path)
	dir := filepath.Dir(path)
	layers := "none"
	if len(row.EvidenceLayers) > 0 {
		layers = strings.Join(row.EvidenceLayers[:min(2, len(row.EvidenceLayers))], "+")
	}
	for _, reason := range row.Reasons {
		if strings.HasPrefix(reason, "path:") {
			layers = strings.Join([]string{layers, reason}, "+")
			break
		}
	}
	if dir == "." {
		ext := filepath.Ext(path)
		if ext == "" {
			ext = filepath.Base(path)
		}
		return dir + "|" + ext + "|" + layers
	}
	return dir + "|" + layers
}

func sourceEvidenceIntersections(row sourceRow) int {
	count := 0
	for _, layer := range row.EvidenceLayers {
		if layer != "path-risk" && layer != "content-risk" && layer != "work-marker" && layer != "secret-risk" && layer != "low-signal" {
			count++
		}
	}
	if row.PathRisk > 0 {
		count++
	}
	if row.ContentRisk > 0 {
		count++
	}
	return count
}

func sourceNeedsMoreEvidence(row sourceRow) bool {
	if row.Score < 3 || len(row.EvidenceLayers) <= 1 {
		return row.Score >= 3
	}
	for _, layer := range row.EvidenceLayers {
		if layer != "path-risk" && layer != "content-risk" && layer != "work-marker" && layer != "secret-risk" {
			return false
		}
	}
	return true
}
