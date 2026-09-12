package app

import (
	"context"
	"time"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/review"
	"github.com/dotcommander/pan/internal/scan"
)

// composeReview derives the deterministic audit report from one finalized
// snapshot. It is shared by one-shot report commands and long-lived agent
// serve sessions so both answer from identical evidence.
func composeReview(ctx context.Context, snap analyze.Snapshot, top int) (review.Report, error) {
	risks, err := scan.Risk(ctx, snap, 0)
	if err != nil {
		return review.Report{}, err
	}
	surface, err := scan.Surface(ctx, snap, 0)
	if err != nil {
		return review.Report{}, err
	}
	effects, err := scan.Effects(ctx, snap, 0)
	if err != nil {
		return review.Report{}, err
	}
	hygiene := scan.Hygiene(ctx, snap.Root, snap)
	changes := scan.Changes(ctx, snap.Root, defaultReviewChangeDays, 0, time.Time{})
	return review.Compose(review.Packets{Overview: scan.Overview(snap), Risks: risks, Surface: surface, Effects: effects, Hygiene: hygiene, Changes: changes, Paths: review.ReportPaths(snap.Root, snap.Files)}, top), nil
}
