package improve

import (
	"context"
	"sort"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/review"
	"github.com/dotcommander/pan/internal/scan"
)

const improveAuditSource = "pan repository audit"

// AuditContext is the bounded repository-map evidence included in proposal
// and run packets. It has the same stable categories as Pan improvement's optional
// Pan audit context, while deriving them from Pan's local scanner.
type AuditContext struct {
	Source          string   `json:"source"`
	RiskLanes       []string `json:"risk_lanes,omitempty"`
	SurfaceKinds    []string `json:"surface_kinds,omitempty"`
	EffectKinds     []string `json:"effect_kinds,omitempty"`
	FirstReadGroups []string `json:"first_read_groups,omitempty"`
	ReviewVerify    []string `json:"review_verify,omitempty"`
}

// BuildAuditContext returns optional deterministic repository evidence. Scan
// failures leave the context absent so diagnostics cannot block a guarded
// improvement run, matching Pan improvement's optional audit integration.
func BuildAuditContext(ctx context.Context, root string, exclude []string) *AuditContext {
	if root == "" {
		return nil
	}
	cfg := config.Default()
	cfg.Exclude = append(cfg.Exclude, exclude...)
	snap, err := analyze.Build(ctx, root, cfg)
	if err != nil {
		return nil
	}
	risks, err := scan.Risk(ctx, snap, maxCandidateFiles)
	if err != nil {
		return nil
	}
	surface, err := scan.Surface(ctx, snap, maxCandidateFiles)
	if err != nil {
		return nil
	}
	effects, err := scan.EffectsWithOptions(ctx, snap, scan.EffectsOptions{Limit: maxCandidateFiles})
	if err != nil {
		return nil
	}
	report := review.Compose(review.Packets{Risks: risks, Surface: surface, Effects: effects, Paths: review.ReportPaths(snap.Root, snap.Files)}, maxCandidateFiles)
	audit := AuditContext{
		Source:          improveAuditSource,
		RiskLanes:       auditRiskLanes(risks),
		SurfaceKinds:    auditSurfaceKinds(surface),
		EffectKinds:     auditEffectKinds(effects),
		FirstReadGroups: auditFirstReadGroups(report.ReadQueue),
		ReviewVerify:    auditVerifyCommands(root),
	}
	if len(audit.RiskLanes)+len(audit.SurfaceKinds)+len(audit.EffectKinds)+len(audit.FirstReadGroups)+len(audit.ReviewVerify) == 0 {
		return nil
	}
	return &audit
}

func auditRiskLanes(report scan.RiskReport) []string {
	values := make([]string, 0, len(report.Lanes))
	for _, lane := range report.Lanes {
		values = append(values, lane.Name)
	}
	return sortedUnique(values)
}

func auditSurfaceKinds(report scan.SurfaceReport) []string {
	values := make([]string, 0)
	for _, file := range report.Files {
		values = append(values, file.Kinds...)
	}
	return sortedUnique(values)
}

func auditEffectKinds(report scan.EffectsReport) []string {
	values := make([]string, 0, len(report.Kinds))
	for _, kind := range report.Kinds {
		values = append(values, kind.Name)
	}
	return sortedUnique(values)
}

func auditFirstReadGroups(queue []review.ReadItem) []string {
	values := make([]string, 0, len(queue))
	for _, item := range queue {
		values = append(values, item.Lane)
	}
	return sortedUnique(values)
}

func auditVerifyCommands(root string) []string {
	verify := providerContextVerifyCommands(root)
	return sortedUnique([]string{verify.Build, verify.Test, verify.Vet})
}

func sortedUnique(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" && value != "(unknown)" {
			set[value] = struct{}{}
		}
	}
	values = values[:0]
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
