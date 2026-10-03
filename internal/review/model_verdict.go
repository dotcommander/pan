package review

import "errors"

// ModelVerdict annotates immutable deterministic evidence. Only successful
// verdicts carry a score; failed or inconclusive judgments never rescore it.
type ModelVerdict struct {
	Status  string   `json:"status"`
	Score   int      `json:"score,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Reasons []string `json:"reasons,omitempty"`
	Detail  string   `json:"detail,omitempty"`
}

func validateModelVerdict(verdict *ModelVerdict) error {
	if verdict == nil {
		return nil
	}
	switch verdict.Status {
	case "success":
		if verdict.Score < minModelScore || verdict.Score > maxModelScore || verdict.Detail != "" {
			return errors.New("invalid successful model verdict")
		}
	case "inconclusive", "error":
		if verdict.Score != 0 || verdict.Summary != "" || len(verdict.Reasons) != 0 {
			return errors.New("invalid unsuccessful model verdict")
		}
	default:
		return errors.New("invalid model verdict status")
	}
	return nil
}
