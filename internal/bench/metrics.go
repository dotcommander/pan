package bench

import (
	"math"
	"slices"
)

// RecallKs are the fixed recall windows every system is scored against.
var RecallKs = []int{1, 5, 10, 20}

// Metrics is one ranked list's score against a gold set: recall at the fixed
// windows, the reciprocal rank of the first gold hit, and NDCG@10 with
// binary relevance.
type Metrics struct {
	RecallAt1  float64 `json:"recall_at_1"`
	RecallAt5  float64 `json:"recall_at_5"`
	RecallAt10 float64 `json:"recall_at_10"`
	RecallAt20 float64 `json:"recall_at_20"`
	MRR        float64 `json:"mrr"`
	NDCGAt10   float64 `json:"ndcg_at_10"`
}

// Score ranks one system's ranked path list against the gold paths. Ties in
// the input order are respected as given; scoring never reorders.
func Score(ranked []string, gold []string) Metrics {
	goldSet := make(map[string]struct{}, len(gold))
	for _, path := range gold {
		goldSet[path] = struct{}{}
	}
	var metrics Metrics
	if len(goldSet) == 0 {
		return metrics
	}
	found := 0
	for k := range RecallKs {
		window := RecallKs[k]
		found = 0
		for _, path := range ranked[:min(window, len(ranked))] {
			if _, ok := goldSet[path]; ok {
				found++
			}
		}
		setRecall(&metrics, window, ratio(found, len(goldSet)))
	}
	for i, path := range ranked {
		if _, ok := goldSet[path]; ok {
			metrics.MRR = 1 / float64(i+1)
			break
		}
	}
	metrics.NDCGAt10 = ndcgAt10(ranked, goldSet)
	return metrics
}

func setRecall(metrics *Metrics, window int, value float64) {
	switch window {
	case 1:
		metrics.RecallAt1 = value
	case 5:
		metrics.RecallAt5 = value
	case 10:
		metrics.RecallAt10 = value
	case 20:
		metrics.RecallAt20 = value
	}
}

func ratio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

// ndcgAt10 scores the first ten ranks with binary relevance and a
// logarithmic position discount.
func ndcgAt10(ranked []string, gold map[string]struct{}) float64 {
	relevant := 0
	for _, path := range ranked[:min(10, len(ranked))] {
		if _, ok := gold[path]; ok {
			relevant++
		}
	}
	if relevant == 0 {
		return 0
	}
	dcg := 0.0
	for i, path := range ranked[:min(10, len(ranked))] {
		if _, ok := gold[path]; ok {
			dcg += 1 / math.Log2(float64(i+2))
		}
	}
	idcg := 0.0
	for i := range min(relevant, 10) {
		idcg += 1 / math.Log2(float64(i+2))
	}
	return dcg / idcg
}

// MeanMetrics averages metric values over scored systems, defining an empty
// mean as zero so aggregates stay deterministic.
func MeanMetrics(values []Metrics) Metrics {
	if len(values) == 0 {
		return Metrics{}
	}
	var sum Metrics
	for _, value := range values {
		sum.RecallAt1 += value.RecallAt1
		sum.RecallAt5 += value.RecallAt5
		sum.RecallAt10 += value.RecallAt10
		sum.RecallAt20 += value.RecallAt20
		sum.MRR += value.MRR
		sum.NDCGAt10 += value.NDCGAt10
	}
	n := float64(len(values))
	sum.RecallAt1 /= n
	sum.RecallAt5 /= n
	sum.RecallAt10 /= n
	sum.RecallAt20 /= n
	sum.MRR /= n
	sum.NDCGAt10 /= n
	return sum
}

// RankedPaths bounds one ranked list for report rows, preserving order.
func RankedPaths(ranked []string, cap int) []string {
	if cap > 0 && len(ranked) > cap {
		return slices.Clone(ranked[:cap])
	}
	return slices.Clone(ranked)
}
