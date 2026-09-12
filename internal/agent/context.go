package agent

import (
	"context"
	"errors"

	"github.com/dotcommander/pan/internal/review"
)

// ContextSchema identifies bounded context packets.
const ContextSchema = "pan.agent-context/v1"

var (
	// ErrStaleEvidence reports evidence that changed after capture.
	ErrStaleEvidence = errors.New("agent stale evidence")
	// ErrContextBudgetTooSmall reports an envelope that cannot fit.
	ErrContextBudgetTooSmall = errors.New("agent context budget too small")
	// ErrContextBudgetTooLarge reports a requested budget over the protocol cap.
	ErrContextBudgetTooLarge = errors.New("agent context budget too large")
)

// ContextPacket is the bounded evidence context returned to an agent.
type ContextPacket struct {
	Schema      string          `json:"schema"`
	ReportID    string          `json:"report_id"`
	BudgetBytes int             `json:"budget_bytes"`
	UsedBytes   int             `json:"used_bytes"`
	Fingerprint string          `json:"fingerprint"`
	Targets     []ContextTarget `json:"targets"`
	Omissions   []string        `json:"omissions,omitempty"`
}

// ContextTarget contains one selected evidence target.
type ContextTarget struct {
	EvidenceID  string   `json:"evidence_id"`
	Path        string   `json:"path"`
	Fingerprint string   `json:"fingerprint"`
	Content     string   `json:"content"`
	Why         []string `json:"why,omitempty"`
}

// ContextBuilder builds one bounded context packet.
type ContextBuilder func(context.Context, review.Document, []review.ReadItem, int) (ContextPacket, error)

// SnapshotVerifier confirms a packet still describes the snapshot.
type SnapshotVerifier func(context.Context, review.Document) error
