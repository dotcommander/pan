package improve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/codemap"
	"github.com/dotcommander/pan/internal/config"
	"github.com/dotcommander/pan/internal/ranking"
	"github.com/dotcommander/pan/internal/retrieval"
)

func (r *providerReader) repoContext(ctx context.Context, args map[string]any) (string, error) {
	action := stringArg(args, "action")
	if action == "" {
		return "", errors.New("repo_context requires action")
	}
	cfg := config.Default()
	cfg.Exclude = append(cfg.Exclude, r.excludes()...)
	snap, err := analyze.Build(ctx, r.root, cfg)
	if err != nil {
		return "", err
	}
	snap = r.filterSnapshot(snap)
	ranked := providerContextRanking(snap, args)
	var result any
	switch action {
	case "map":
		result = codemap.Build(ranked, codemap.Options{Tokens: boundedProviderInt(args, "tokens", 4096, 512, 8192), Root: r.root, Edges: snap.Edges})
	case "impact":
		path := stringArg(args, providerPathKey)
		if _, _, pathErr := r.path(path); pathErr != nil {
			return "", pathErr
		}
		impact, ok := retrieval.Impact(ranked, path)
		if !ok {
			return "", fmt.Errorf("no impact data for %q", path)
		}
		result = impact
	case "symbol":
		symbol := stringArg(args, providerSymbolKey)
		if symbol == "" {
			return "", errors.New("repo_context symbol requires symbol")
		}
		symbolContext, contextErr := retrieval.SymbolContext(snap, ranked, symbol, retrieval.SymbolOptions{MaxSourceLines: 120})
		if contextErr != nil {
			return "", contextErr
		}
		result = symbolContext
	case providerAudit:
		result, err = r.repoContextAudit(ctx, snap, args)
	case providerBrief:
		result, err = r.repoContextBrief(ctx, snap, ranked, args)
	default:
		return "", fmt.Errorf("unknown repo_context action %q", action)
	}
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	limit := boundedProviderInt(args, "max_bytes", r.limit, 1000, 40000)
	if limit < r.limit {
		return capProviderBytes(string(encoded), limit), nil
	}
	return r.cap(string(encoded)), nil
}

// providerContextRanking computes scores only for projections that consume them.
func providerContextRanking(snap analyze.Snapshot, args map[string]any) []ranking.RankedFile {
	switch stringArg(args, providerActionKey) {
	case "map", "impact", providerSymbolKey, providerBrief:
		return ranking.Rank(snap, "", ranking.Options{Intent: stringArg(args, "intent")})
	default:
		return nil
	}
}
