package improve

import (
	"context"
	"sync"
	"time"
)

func prepGenerationRound(ctx context.Context, targets []PrepTarget, accepted map[string]bool, acceptedFiles int, opts PrepOptions) ([]PrepTarget, []*prepGeneration) {
	selected := make([]PrepTarget, 0, len(targets))
	for _, target := range targets {
		if accepted[target.File] || (opts.MaxFiles > 0 && acceptedFiles+len(selected) >= opts.MaxFiles) {
			continue
		}
		selected = append(selected, target)
	}
	generated := make([]*prepGeneration, len(selected))
	if len(selected) < 2 || (!opts.ContinueToFloor && opts.MaxFiles <= 1) || opts.RequestInterval > 0 || opts.TimeBudget > 0 {
		return selected, generated
	}
	limit := opts.MaxConcurrent
	if limit <= 0 {
		limit = 4
	}
	if limit > len(selected) {
		limit = len(selected)
	}
	sem := make(chan struct{}, limit)
	var wait sync.WaitGroup
	for index := range selected {
		wait.Add(1)
		go func() {
			defer wait.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			requestCtx, cancel := prepRequestContext(ctx, opts, "")
			changes, err := opts.Generator.Generate(requestCtx, selected[index], "")
			cancel()
			generated[index] = &prepGeneration{changes: changes, err: err}
		}()
	}
	wait.Wait()
	return selected, generated
}

func waitPrepRequest(ctx context.Context, previous *time.Time, interval time.Duration) error {
	if previous.IsZero() || interval <= 0 {
		return nil
	}
	wait := interval - time.Since(*previous)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func prepRequestContext(ctx context.Context, opts PrepOptions, feedback string) (context.Context, context.CancelFunc) {
	timeout := opts.RequestTimeout
	if feedback != "" && opts.CorrectiveTimeout > 0 {
		timeout = opts.CorrectiveTimeout
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}
