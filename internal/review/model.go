package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/provider"
)

const (
	localModel       = "Qwen3.6-35B-A3B-oQ4-fp16-mtp"
	localBaseURL     = "http://127.0.0.1:8000/v1"
	localProfileEnv  = "PAN_API_KEY"
	modelBatchSize   = 8
	modelCacheLimit  = 5000
	modelResponseCap = 1 << 20
)

// ModelOptions selects optional OpenAI-compatible report scoring. An empty
// Model keeps the report fully deterministic and offline.
type ModelOptions struct {
	Model         string
	BaseURL       string
	APIKeyEnv     string
	Local         bool
	NoCache       bool
	CacheDir      string
	ContentHashes map[string]string
	Prompt        string
}

type modelScore struct {
	Index   int      `json:"index"`
	Score   int      `json:"score"`
	Summary string   `json:"summary"`
	Reasons []string `json:"reasons"`
}

type cachedModelScore struct {
	Score   int
	Summary string
	Reasons []string
}

// Resolve applies the documented loopback profile without reading or writing
// user configuration. An explicit base URL must always accompany model use.
func (o ModelOptions) Resolve() (ModelOptions, error) {
	if o.Local {
		if o.Model == "" {
			o.Model = localModel
		}
		if o.BaseURL == "" {
			o.BaseURL = localBaseURL
		}
		if o.APIKeyEnv == "" {
			o.APIKeyEnv = localProfileEnv
		}
	}
	if o.Prompt == "" {
		o.Prompt = config.Default().ReviewModelPrompt
	}
	if o.Model == "" {
		return o, nil
	}
	if o.BaseURL == "" {
		return ModelOptions{}, errors.New("model scoring requires --base-url")
	}
	return resolveModelCacheDir(o)
}

// Score annotates only the deterministically retained kept queue. Failures
// preserve deterministic evidence and record a separate verdict; a
// cancelled caller context is returned unchanged.
func (o ModelOptions) Score(ctx context.Context, report Report) (Report, error) {
	o, err := o.Resolve()
	if err != nil || o.Model == "" || len(report.ReadQueue) == 0 {
		return report, err
	}
	report.ReadQueue = append([]ReadItem(nil), report.ReadQueue...)
	for i := range report.ReadQueue {
		report.ReadQueue[i].Why = append([]string(nil), report.ReadQueue[i].Why...)
	}
	kept := make([]ReadItem, 0, len(report.ReadQueue))
	for _, row := range report.ReadQueue {
		if row.Lane == LaneKept {
			kept = append(kept, row)
		}
	}
	if len(kept) == 0 {
		return report, nil
	}
	cache := modelScoreCache{entries: map[string]cachedModelScore{}}
	if !o.NoCache {
		loadDiskCache(o, &cache)
	}
	client, err := provider.New(provider.Config{
		BaseURL: o.BaseURL, APIKey: os.Getenv(o.APIKeyEnv), Model: o.Model,
		Timeout: 90 * time.Second, MaxResponseBytes: modelResponseCap,
	})
	if err != nil {
		return Report{}, fmt.Errorf("create model scorer: %w", err)
	}
	for start := 0; start < len(kept); start += modelBatchSize {
		end := min(start+modelBatchSize, len(kept))
		if err := o.scoreBatch(ctx, client, kept[start:end], &cache); err != nil {
			return Report{}, err
		}
	}
	byPath := make(map[string]*ModelVerdict, len(kept))
	for _, row := range kept {
		byPath[row.Path] = row.ModelVerdict
	}
	for i := range report.ReadQueue {
		if verdict, ok := byPath[report.ReadQueue[i].Path]; ok {
			report.ReadQueue[i].ModelVerdict = verdict
		}
	}
	reorder(&report)
	if !o.NoCache {
		persistDiskCache(o, &cache)
	}
	return report, nil
}

func (o ModelOptions) scoreBatch(ctx context.Context, client *provider.Client, rows []ReadItem, cache *modelScoreCache) error {
	missing := make([]int, 0, len(rows))
	for i := range rows {
		if cached, ok := loadModelScore(o, cache, rows[i]); ok {
			applyModelScore(&rows[i], cached)
			continue
		}
		missing = append(missing, i)
	}
	if len(missing) == 0 {
		return nil
	}
	requested := make([]ReadItem, len(missing))
	for index, i := range missing {
		requested[index] = rows[i]
	}
	prompt := o.Prompt
	if prompt == "" {
		prompt = config.Default().ReviewModelPrompt
	}
	response, err := client.Complete(ctx, provider.Request{
		Messages:    []provider.Message{{Role: "user", Content: prompt + modelPrompt(requested)}},
		Temperature: float64Ptr(0),
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		for _, i := range missing {
			degradeModelScore(&rows[i], err)
		}
		return nil
	}
	scores, err := responseScores(response)
	if err != nil {
		for _, i := range missing {
			degradeModelScore(&rows[i], err)
		}
		return nil
	}
	byIndex := make(map[int]modelScore, len(scores))
	for _, score := range scores {
		if score.Index < 0 || score.Index >= len(requested) {
			err = errors.New("provider returned an out-of-range index")
			break
		}
		if _, exists := byIndex[score.Index]; exists {
			err = errors.New("provider returned a duplicate index")
			break
		}
		byIndex[score.Index] = score
	}
	if err != nil {
		for _, i := range missing {
			degradeModelScore(&rows[i], err)
		}
		return nil
	}
	for local, i := range missing {
		score, ok := byIndex[local]
		switch {
		case !ok:
			// The judge answered the batch but abstained on this row.
			markModelInconclusive(&rows[i], "no score returned for this row")
		case score.Score < minModelScore || score.Score > maxModelScore:
			// The judge committed an unusable verdict for this row.
			markModelInconclusive(&rows[i], fmt.Sprintf("score %d outside %d-%d", score.Score, minModelScore, maxModelScore))
		default:
			// Key before applyModelScore mutates the row; cache entries are
			// keyed on the deterministic pre-scoring state.
			key := cacheKey(o, rows[i])
			cached := cachedModelScore{Score: score.Score, Summary: strings.TrimSpace(score.Summary), Reasons: cleanModelReasons(score.Reasons)}
			applyModelScore(&rows[i], cached)
			if o.ContentHashes == nil || o.ContentHashes[rows[i].Path] != "" {
				storeModelScore(o, cache, key, cached)
			}
		}
	}
	return nil
}

func responseScores(response provider.Response) ([]modelScore, error) {
	if len(response.Choices) == 0 {
		return nil, errors.New("provider returned no choices")
	}
	return parseModelScores(response.Choices[0].Message.Content)
}

func modelPrompt(rows []ReadItem) string {
	type row struct {
		Index      int      `json:"index"`
		Path       string   `json:"path"`
		Score      int      `json:"deterministic_score"`
		Why        []string `json:"why"`
		Lane       string   `json:"lane"`
		EvidenceID string   `json:"evidence_id"`
	}
	payload := make([]row, len(rows))
	for i, item := range rows {
		payload[i] = row{i, item.Path, item.Score, item.Why, item.Lane, item.EvidenceID}
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

func parseModelScores(content string) ([]modelScore, error) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "[") {
		return nil, errors.New("provider returned invalid score JSON")
	}
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, errors.New("provider returned invalid score JSON")
	}
	for _, row := range raw {
		if row["index"] == nil || row["score"] == nil || bytes.Equal(row["index"], []byte("null")) || bytes.Equal(row["score"], []byte("null")) {
			return nil, errors.New("provider verdict is missing index or score")
		}
	}
	var scores []modelScore
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&scores); err != nil {
		return nil, errors.New("provider returned invalid score JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("provider returned trailing score JSON")
	}
	return scores, nil
}

func applyModelScore(row *ReadItem, score cachedModelScore) {
	row.ModelVerdict = &ModelVerdict{Status: "success", Score: score.Score, Summary: score.Summary, Reasons: append([]string(nil), score.Reasons...)}
}

// Model score contract: the judge returns one score per row within
// [minModelScore, maxModelScore] (see modelPrompt).
const (
	minModelScore = 1
	maxModelScore = 5
)

// markModelInconclusive records that the model judge did not commit a usable
// verdict for a row: it either omitted the row from an otherwise valid
// response or returned a score outside the contract. The deterministic score
// is retained so ranking is unchanged, and the recorded reason distinguishes
// "the judge did not decide" (model_inconclusive) from "the pipe broke"
// (model_error). Abstentions are deliberately not cached; the row is asked
// again on the next run.
func markModelInconclusive(row *ReadItem, detail string) {
	row.ModelVerdict = &ModelVerdict{Status: "inconclusive", Detail: sanitizeModelDetail(detail)}
}

func degradeModelScore(row *ReadItem, err error) {
	row.ModelVerdict = &ModelVerdict{Status: "error", Detail: safeModelError(err)}
}
func safeModelError(err error) string {
	if err == nil {
		return "unknown"
	}
	var httpErr *provider.HTTPError
	if errors.As(err, &httpErr) {
		return fmt.Sprintf("provider HTTP %d", httpErr.StatusCode)
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline exceeded"
	}
	return "provider or verdict validation failed"
}
func sanitizeModelDetail(detail string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(detail)
}
func cleanModelReasons(reasons []string) []string {
	result := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if reason = strings.TrimSpace(reason); reason != "" {
			result = append(result, reason)
		}
	}
	return result
}
func float64Ptr(value float64) *float64 { return &value }

// Successful verdicts move only among successful kept positions. All other
// rows retain their deterministic positions and every row retains its lane.
func reorder(report *Report) {
	positions := []int{}
	rows := []ReadItem{}
	for i, row := range report.ReadQueue {
		if row.Lane == LaneKept && row.ModelVerdict != nil && row.ModelVerdict.Status == "success" {
			positions = append(positions, i)
			rows = append(rows, row)
		}
	}
	slices.SortStableFunc(rows, func(a, b ReadItem) int { return b.ModelVerdict.Score - a.ModelVerdict.Score })
	for i, position := range positions {
		report.ReadQueue[position] = rows[i]
	}
	for i := range report.ReadQueue {
		report.ReadQueue[i].Rank = i + 1
	}
	if len(report.Rationale) > 0 {
		report.Rationale = rationale(report.ReadQueue, len(report.Rationale))
	}
	if report.CullLedger != nil {
		ledger := *report.CullLedger
		existing := make(map[string]CullEntry, len(ledger.Entries))
		for _, entry := range ledger.Entries {
			existing[entry.Path] = entry
		}
		ledger.Entries = make([]CullEntry, len(report.ReadQueue))
		for i, row := range report.ReadQueue {
			entry := existing[row.Path]
			entry.Rank = row.Rank
			ledger.Entries[i] = entry
		}
		report.CullLedger = &ledger
	}
}
