package agent

import "github.com/dotcommander/pan/internal/review"

func fixedDocument() review.Document {
	return review.Document{Schema: review.DocumentSchema, ReportID: "report-1", ReadQueue: []review.ReadItem{{EvidenceID: "ev-1", Path: "main.go"}}}
}
