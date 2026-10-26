package webtoon

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

const (
	maxBackoff                = 30 * time.Second
	maxRateLimitWait          = 1 * time.Minute
	maxParseableRetrySecs     = 365 * 24 * 60 * 60
	maxParseableRetryDuration = time.Duration(maxParseableRetrySecs) * time.Second
	defaultMaxRetries         = 4
	defaultBaseRetryDelay     = 1 * time.Second
)

// ResponseAction indicates how the client should react to an HTTP status code.
type ResponseAction int

const (
	// ActionRetry indicates a transient server error; retry with constant base delay.
	ActionRetry ResponseAction = iota
	// ActionRateLimit indicates HTTP 429; retry with exponential backoff.
	ActionRateLimit
	// ActionAbort indicates a permanent error (e.g. 400, 401, 403, 404); do not retry.
	ActionAbort
)

// ClassifyStatus maps an HTTP status code to the appropriate ResponseAction.
func ClassifyStatus(statusCode int) ResponseAction {
	switch {
	case statusCode == http.StatusRequestTimeout:
		return ActionRetry
	case statusCode == http.StatusTooManyRequests:
		return ActionRateLimit
	case statusCode >= 520 && statusCode <= 530:
		return ActionRetry
	case statusCode >= 400 && statusCode < 500:
		return ActionAbort
	case statusCode >= 500:
		return ActionRetry
	default:
		return ActionAbort
	}
}

// SleepContext performs a context-aware sleep. Returns false if the context was canceled early.
func SleepContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// RetryDelay computes the delay before the next retry attempt based on the action.
func RetryDelay(action ResponseAction, attempt int, baseDelay time.Duration) time.Duration {
	switch action {
	case ActionAbort:
		return 0
	case ActionRetry:
		return baseDelay
	case ActionRateLimit:
		shift := min(max(attempt, 0), 4)
		return min(baseDelay<<uint(shift), maxBackoff)
	default:
		return 0
	}
}

var timeNow = time.Now

func parseRateLimitDelay(resp *http.Response, fallback time.Duration) time.Duration {
	if retryAfterStr := resp.Header.Get("Retry-After"); retryAfterStr != "" {
		if retryAfterSecs, err := strconv.Atoi(retryAfterStr); err == nil && retryAfterSecs > 0 {
			return time.Duration(min(retryAfterSecs, maxParseableRetrySecs))*time.Second + time.Second
		}
		if targetTime, err := http.ParseTime(retryAfterStr); err == nil {
			if diff := targetTime.Sub(timeNow()); diff > 0 {
				return min(diff, maxParseableRetryDuration) + time.Second
			}
		}
	}
	if resetStr := resp.Header.Get("X-RateLimit-Reset"); resetStr != "" {
		if resetUnix, err := strconv.ParseInt(resetStr, 10, 64); err == nil {
			nowUnix := timeNow().Unix()
			if resetUnix > nowUnix {
				diffSecs := min(resetUnix-nowUnix, maxParseableRetrySecs)
				return time.Duration(diffSecs)*time.Second + time.Second
			}
		}
	}
	return fallback
}
