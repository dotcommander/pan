package provider

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const defaultProviderBackoff = 30 * time.Second

func retryableHTTPError(err error) bool {
	var status *HTTPError
	if !errors.As(err, &status) {
		return false
	}
	switch status.StatusCode {
	case 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}

func retryDelay(err error, attempt int, maxBackoff, rateLimitBackoff time.Duration) time.Duration {
	var status *HTTPError
	if errors.As(err, &status) && status.StatusCode == 429 {
		if after, parseErr := http.ParseTime(status.RetryAfter); parseErr == nil {
			return capBackoff(time.Until(after), maxBackoff)
		}
		if seconds, parseErr := time.ParseDuration(status.RetryAfter + "s"); parseErr == nil {
			return capBackoff(seconds, maxBackoff)
		}
		if rateLimitBackoff > 0 {
			return capBackoff(rateLimitBackoff, maxBackoff)
		}
	}
	if attempt > 5 {
		attempt = 5
	}
	return capBackoff(time.Second<<attempt, maxBackoff)
}

func capBackoff(delay, maximum time.Duration) time.Duration {
	if delay < 0 {
		return 0
	}
	if maximum <= 0 {
		maximum = defaultProviderBackoff
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
