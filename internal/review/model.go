package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dotcommander/pan/internal/provider"
)

const (
	localModel       = "Qwen3.6-35B-A3B-oQ4-fp16-mtp"
	localBaseURL     = "http://127.0.0.1:8000/v1"
	localAPIKeyEnv   = "PAN_API_KEY"
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
			o.APIKeyEnv = localAPIKeyEnv
		}
	}
	if o.Model == "" {
		return o, nil
	}
	if o.BaseURL == "" {
		return ModelOptions{}, errors.New("model scoring requires --base-url")
	}
	return resolveModelCacheDir(o)
}

// Score applies model scores to the current read queue. Provider or response
// failures retain deterministic scores and record a model_error reason; a
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
	for start := 0; start < len(report.ReadQueue); start += modelBatchSize {
		end := min(start+modelBatchSize, len(report.ReadQueue))
		if err := o.scoreBatch(ctx, client, report.ReadQueue[start:end], &cache); err != nil {
			return Report{}, err
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
	response, err := client.Complete(ctx, provider.Request{
		Messages:    []provider.Message{{Role: "user", Content: modelPrompt(rows)}},
		Temperature: float64Ptr(0), ResponseFormat: map[string]string{"type": "json_object"},
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
		byIndex[score.Index] = score
	}
	for _, i := range missing {
		key := cacheKey(o, rows[i])
		score, ok := byIndex[i]
		if !ok || score.Score < 1 || score.Score > 5 {
			degradeModelScore(&rows[i], errors.New("invalid or missing model score"))
			continue
		}
		cached := cachedModelScore{Score: score.Score, Summary: strings.TrimSpace(score.Summary), Reasons: cleanModelReasons(score.Reasons)}
		applyModelScore(&rows[i], cached)
		storeModelScore(o, cache, key, cached)
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
		Index int      `json:"index"`
		Path  string   `json:"path"`
		Score int      `json:"deterministic_score"`
		Why   []string `json:"why"`
	}
	payload := make([]row, len(rows))
	for i, item := range rows {
		payload[i] = row{i, item.Path, item.Score, item.Why}
	}
	data, _ := json.Marshal(payload)
	return "Score each review candidate from 1 to 5. Return only a JSON array: [{\"index\":0,\"score\":1,\"summary\":\"...\",\"reasons\":[\"...\"]}]. Do not include secrets. Candidates: " + string(data)
}

func parseModelScores(content string) ([]modelScore, error) {
	content = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(content), "```json"), "```"))
	var scores []modelScore
	if err := json.Unmarshal([]byte(content), &scores); err == nil && len(scores) > 0 {
		return scores, nil
	}
	return nil, errors.New("provider returned invalid score JSON")
}

func applyModelScore(row *ReadItem, score cachedModelScore) {
	row.Score, row.EvidenceID = score.Score, ""
	row.Why = appendUnique(row.Why, "model:score")
	row.Why = append(row.Why, score.Reasons...)
	row.EvidenceID = EvidenceIdentity(*row)
}

func degradeModelScore(row *ReadItem, err error) {
	row.Why = appendUnique(row.Why, "model_error:"+safeModelError(err))
	row.EvidenceID = EvidenceIdentity(*row)
}
func safeModelError(err error) string {
	if err == nil {
		return "unknown"
	}
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(err.Error())
}
func cleanModelReasons(reasons []string) []string {
	for i := range reasons {
		reasons[i] = "model:" + strings.TrimSpace(reasons[i])
	}
	return slices.DeleteFunc(reasons, func(s string) bool { return s == "model:" })
}
func float64Ptr(value float64) *float64 { return &value }

func reorder(report *Report) {
	slices.SortFunc(report.ReadQueue, func(a, b ReadItem) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return strings.Compare(a.Path, b.Path)
	})
	for i := range report.ReadQueue {
		report.ReadQueue[i].Rank = i + 1
		report.ReadQueue[i].Lane = CullDispositions(report.ReadQueue[i : i+1])[0].Lane
		report.ReadQueue[i].EvidenceID = EvidenceIdentity(report.ReadQueue[i])
	}
}
