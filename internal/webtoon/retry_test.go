package webtoon_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/underhax/go-webtoons-downloader/internal/webtoon"
)

func TestClassifyStatusTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		wantAction webtoon.ResponseAction
	}{
		{name: "rate_limit_429", status: http.StatusTooManyRequests, wantAction: webtoon.ActionRateLimit},
		{name: "request_timeout_408", status: http.StatusRequestTimeout, wantAction: webtoon.ActionRetry},
		{name: "server_error_500", status: http.StatusInternalServerError, wantAction: webtoon.ActionRetry},
		{name: "bad_gateway_502", status: http.StatusBadGateway, wantAction: webtoon.ActionRetry},
		{name: "cloudflare_525", status: 525, wantAction: webtoon.ActionRetry},
		{name: "bad_request_400", status: http.StatusBadRequest, wantAction: webtoon.ActionAbort},
		{name: "not_found_404", status: http.StatusNotFound, wantAction: webtoon.ActionAbort},
		{name: "success_200", status: http.StatusOK, wantAction: webtoon.ActionAbort},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := webtoon.ClassifyStatus(tc.status)
			if got != tc.wantAction {
				t.Errorf("status %d: expected action %v, got %v", tc.status, tc.wantAction, got)
			}
		})
	}
}

func TestRetryDelayTable(t *testing.T) {
	t.Parallel()

	base := 1 * time.Second

	tests := []struct {
		name      string
		action    webtoon.ResponseAction
		attempt   int
		wantDelay time.Duration
	}{
		{name: "abort_zero", action: webtoon.ActionAbort, attempt: 0, wantDelay: 0},
		{name: "retry_constant", action: webtoon.ActionRetry, attempt: 3, wantDelay: base},
		{name: "rate_limit_attempt_0", action: webtoon.ActionRateLimit, attempt: 0, wantDelay: 1 * time.Second},
		{name: "rate_limit_attempt_1", action: webtoon.ActionRateLimit, attempt: 1, wantDelay: 2 * time.Second},
		{name: "rate_limit_attempt_2", action: webtoon.ActionRateLimit, attempt: 2, wantDelay: 4 * time.Second},
		{name: "rate_limit_attempt_3", action: webtoon.ActionRateLimit, attempt: 3, wantDelay: 8 * time.Second},
		{name: "rate_limit_capped", action: webtoon.ActionRateLimit, attempt: 10, wantDelay: 16 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := webtoon.RetryDelay(tc.action, tc.attempt, base)
			if got != tc.wantDelay {
				t.Errorf("expected delay %v, got %v", tc.wantDelay, got)
			}
		})
	}
}

func TestSleepContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if webtoon.SleepContext(ctx, 100*time.Millisecond) {
		t.Errorf("expected canceled context to return false")
	}

	okCtx := context.Background()
	if !webtoon.SleepContext(okCtx, 0) {
		t.Errorf("expected non-canceled context with 0 delay to return true")
	}
}
