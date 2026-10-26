package webtoon

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

type mockRoundTripFunc func(req *http.Request) (*http.Response, error)

func (m mockRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req)
}

type webtoonFailReadCloser struct{}

func (f *webtoonFailReadCloser) Read(_ []byte) (int, error) {
	return 0, errors.New("simulated body read error")
}

func (f *webtoonFailReadCloser) Close() error {
	return nil
}

type webtoonFailCloserOnly struct {
	io.Reader
}

func (f *webtoonFailCloserOnly) Close() error {
	return errors.New("simulated body close error")
}

type webtoonFailReadAndCloseError struct{}

func (f *webtoonFailReadAndCloseError) Read(_ []byte) (int, error) {
	return 0, errors.New("simulated body read error")
}

func (f *webtoonFailReadAndCloseError) Close() error {
	return errors.New("simulated body close error")
}

func mockFailReadAndCloseTransport(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       &webtoonFailReadAndCloseError{},
	}, nil
}

func mockInstantSleepContext(ctx context.Context, _ time.Duration) bool {
	return ctx.Err() == nil
}

type attemptCounterHolder struct {
	count int
}

func (h *attemptCounterHolder) roundTripRetryThenSuccess(_ *http.Request) (*http.Response, error) {
	h.count++
	if h.count == 1 {
		return nil, errors.New("transient network error")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("ok")),
	}, nil
}

func mockFailingChapterViewerTransport(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader("server error")),
	}, nil
}

type cancelFuncHolder struct {
	cancel context.CancelFunc
}

func (h *cancelFuncHolder) roundTripNetworkFail(_ *http.Request) (*http.Response, error) {
	h.cancel()
	return nil, errors.New("simulated network failure")
}

func (h *cancelFuncHolder) roundTripRetryFail(_ *http.Request) (*http.Response, error) {
	h.cancel()
	return &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader("server error")),
	}, nil
}

func mockInternalClientTransport(_ *http.Request) (*http.Response, error) {
	body := `<html><head><link rel="canonical" href="https://www.webtoons.com/en/fantasy/tower/list?title_no=95"><meta property="og:title" content="Tower of God"></head><body></body></html>`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/html"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func mockInvalidJSONTransport(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader("invalid-json")),
	}, nil
}

func mockFailingBodyReadTransport(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       &webtoonFailReadCloser{},
	}, nil
}

func mockAbortCloseFailTransport(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       &webtoonFailCloserOnly{Reader: strings.NewReader("not found")},
	}, nil
}

func mockRateLimitTransport(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"0"}},
		Body:       io.NopCloser(strings.NewReader("rate limited")),
	}, nil
}

func mockRetryCloseFailTransport(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       &webtoonFailCloserOnly{Reader: strings.NewReader("server error")},
	}, nil
}

func fixedTestTime() time.Time {
	return time.Date(2026, time.October, 26, 12, 0, 0, 0, time.UTC)
}

func TestParseRateLimitDelayTable(t *testing.T) {
	origNow := timeNow
	timeNow = fixedTestTime
	defer func() {
		timeNow = origNow
	}()

	futureTime := fixedTestTime().Add(10 * time.Second)
	pastTime := fixedTestTime().Add(-10 * time.Second)
	fallback := 3 * time.Second

	tests := []struct {
		name         string
		headerVal    string
		minDelay     time.Duration
		isRetryAfter bool
		isReset      bool
		isFallback   bool
	}{
		{
			minDelay:     5 * time.Second,
			name:         "retry after seconds valid",
			headerVal:    "5",
			isRetryAfter: true,
		},
		{
			minDelay:     maxParseableRetryDuration,
			name:         "retry after huge seconds capped without overflow",
			headerVal:    "9223372037",
			isRetryAfter: true,
		},
		{
			minDelay:     5 * time.Second,
			name:         "retry after http date in future",
			headerVal:    futureTime.UTC().Format(http.TimeFormat),
			isRetryAfter: true,
		},
		{
			name:         "retry after http date in past falls back",
			headerVal:    pastTime.UTC().Format(http.TimeFormat),
			isRetryAfter: true,
			isFallback:   true,
		},
		{
			name:         "retry after invalid string falls back",
			headerVal:    "invalid-date-or-num",
			isRetryAfter: true,
			isFallback:   true,
		},
		{
			minDelay:   5 * time.Second,
			name:       "rate limit reset in future",
			headerVal:  strconv.FormatInt(futureTime.Unix(), 10),
			isReset:    true,
			isFallback: false,
		},
		{
			minDelay:   maxParseableRetryDuration,
			name:       "rate limit reset huge unix timestamp capped without overflow",
			headerVal:  "9223372036854775807",
			isReset:    true,
			isFallback: false,
		},
		{
			name:       "rate limit reset in past falls back",
			headerVal:  strconv.FormatInt(pastTime.Unix(), 10),
			isReset:    true,
			isFallback: true,
		},
		{
			name:       "rate limit reset invalid string falls back",
			headerVal:  "not-a-number",
			isReset:    true,
			isFallback: true,
		},
		{
			name:       "no headers returns fallback",
			isFallback: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{Header: make(http.Header)}
			if tc.isRetryAfter {
				resp.Header.Set("Retry-After", tc.headerVal)
			}
			if tc.isReset {
				resp.Header.Set("X-RateLimit-Reset", tc.headerVal)
			}

			got := parseRateLimitDelay(resp, fallback)
			if tc.isFallback && got != fallback {
				t.Errorf("expected fallback %v, got %v", fallback, got)
			}
			if !tc.isFallback && got < tc.minDelay {
				t.Errorf("expected at least %v, got %v", tc.minDelay, got)
			}
		})
	}
}

func TestParserInternalHelpers(t *testing.T) {
	t.Parallel()

	t.Run("is allowed webtoon host variations", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			host     string
			expected bool
		}{
			{name: "standard host", host: "webtoons.com", expected: true},
			{name: "www subdomain", host: "www.webtoons.com", expected: true},
			{name: "mobile subdomain", host: "m.webtoons.com", expected: true},
			{name: "uppercase host", host: "WWW.WEBTOONS.COM", expected: true},
			{name: "host with port", host: "www.webtoons.com:443", expected: true},
			{name: "host with trailing dot", host: "www.webtoons.com.", expected: true},
			{name: "host with port and trailing dot", host: "www.webtoons.com.:443", expected: true},
			{name: "disallowed domain", host: "disallowed.example", expected: false},
			{name: "subdomain of disallowed", host: "webtoons.example.com", expected: false},
			{name: "empty host", host: "", expected: false},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got := isAllowedWebtoonHost(tc.host)
				if got != tc.expected {
					t.Errorf("host %s: expected %v, got %v", tc.host, tc.expected, got)
				}
			})
		}
	})

	t.Run("extract viewer container variations", func(t *testing.T) {
		t.Parallel()

		noCloseHTML := "<p>No closing div here</p>"
		if got := extractViewerContainer(noCloseHTML); got != noCloseHTML {
			t.Errorf("expected unclosed div to return raw string")
		}

		nestedHTML := "<div>inner</div></div>trailing"
		gotNested := extractViewerContainer(nestedHTML)
		if gotNested != "<div>inner</div>" {
			t.Errorf("unexpected nested container extraction: %q", gotNested)
		}
	})

	t.Run("retry delay default action", func(t *testing.T) {
		t.Parallel()
		got := RetryDelay(ResponseAction(999), 0, time.Second)
		if got != 0 {
			t.Errorf("expected 0 for unknown action, got %v", got)
		}
	})
}

func TestSleepContextSuccess(t *testing.T) {
	t.Parallel()

	if !SleepContext(context.Background(), 1*time.Millisecond) {
		t.Errorf("expected SleepContext to return true when timer fires")
	}
}

func TestParserEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("empty host invalid url", func(t *testing.T) {
		t.Parallel()
		_, err := ParseSeriesURL("http://")
		if err == nil || !errors.Is(err, ErrInvalidURL) {
			t.Errorf("expected ErrInvalidURL for http://, got %v", err)
		}
	})

	t.Run("missing path segments", func(t *testing.T) {
		t.Parallel()
		_, err := ParseSeriesURL("https://www.webtoons.com/")
		if err == nil || !strings.Contains(err.Error(), "invalid path structure") {
			t.Errorf("expected invalid path structure error, got %v", err)
		}
	})

	t.Run("invalid title_no zero parsed", func(t *testing.T) {
		t.Parallel()
		pageHTML := `<link rel="canonical" href="https://example.com?title_no=0">`
		_, err := ExtractTitleNo(pageHTML)
		if err == nil || !strings.Contains(err.Error(), "invalid title_no parsed: 0") {
			t.Errorf("expected invalid title_no error, got %v", err)
		}
	})

	t.Run("og title content first with single quotes", func(t *testing.T) {
		t.Parallel()
		pageHTML := `<meta content='SingleQuoteTitle' property="og:title">`
		title := extractMetaOGTitle(pageHTML)
		if title != "SingleQuoteTitle" {
			t.Errorf("expected 'SingleQuoteTitle', got %q", title)
		}
	})

	t.Run("extract image urls with empty url strings", func(t *testing.T) {
		t.Parallel()
		pageHTML := `<div class="viewer_img _img_viewer_area"><img data-url=""></div>`
		_, err := ExtractImageURLs(pageHTML)
		if err == nil || !errors.Is(err, ErrNoImagesFound) {
			t.Errorf("expected ErrNoImagesFound for empty img data-url, got %v", err)
		}
	})

	t.Run("extract viewer container unclosed trailing div", func(t *testing.T) {
		t.Parallel()
		pageHTML := "<div </div> <div"
		got := extractViewerContainer(pageHTML)
		if got != pageHTML {
			t.Errorf("expected raw string for unclosed trailing div, got %q", got)
		}
	})
}

func TestClientEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("fetch metadata with scheme-less rawURL", func(t *testing.T) {
		t.Parallel()
		client := NewClient(
			time.Second,
			WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(mockInternalClientTransport)}),
		)
		info, err := client.FetchSeriesMetadata(context.Background(), "www.webtoons.com/en/fantasy/tower/list?title_no=95")
		if err != nil || info.TitleName == "" {
			t.Errorf("expected metadata with scheme-less url, got %v, err: %v", info, err)
		}
	})

	t.Run("fetch chapters invalid json", func(t *testing.T) {
		t.Parallel()
		client := NewClient(
			time.Second,
			WithBaseURLs("https://www.example.com", "https://m.example.com"),
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(mockInvalidJSONTransport)}),
		)
		info := &SeriesInfo{SeriesType: "webtoon", TitleNo: 95, Language: "en"}
		_, err := client.FetchChapters(context.Background(), info)
		if err == nil || !strings.Contains(err.Error(), "decode episodes json") {
			t.Errorf("expected decode episodes json error, got %v", err)
		}
	})

	t.Run("download image read failure", func(t *testing.T) {
		t.Parallel()
		client := NewClient(
			time.Second,
			WithBaseURLs("https://www.example.com", "https://m.example.com"),
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(mockFailingBodyReadTransport)}),
		)
		_, err := client.DownloadImage(context.Background(), "https://www.example.com/img/fail.jpg")
		if err == nil || !strings.Contains(err.Error(), "read image body") {
			t.Errorf("expected read image body error, got %v", err)
		}
	})

	t.Run("getHTML read failure", func(t *testing.T) {
		t.Parallel()
		client := NewClient(
			time.Second,
			WithBaseURLs("https://www.example.com", "https://m.example.com"),
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(mockFailingBodyReadTransport)}),
		)
		_, err := client.getHTML(context.Background(), "https://www.example.com/fail.html", "")
		if err == nil || !strings.Contains(err.Error(), "read html response") {
			t.Errorf("expected read html response error, got %v", err)
		}
	})

	t.Run("executeWithRetry invalid target url", func(t *testing.T) {
		t.Parallel()
		client := NewClient(time.Second)
		resp, err := client.executeWithRetry(context.Background(), "://invalid-url", "")
		if resp != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Errorf("unexpected close error: %v", cerr)
			}
		}
		if err == nil || !strings.Contains(err.Error(), "create request") {
			t.Errorf("expected create request error, got %v", err)
		}
	})
}

func TestClientRetryErrors(t *testing.T) {
	t.Parallel()

	t.Run("executeWithRetry network error and canceled retry delay", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		holder := &cancelFuncHolder{cancel: cancel}
		client := NewClient(
			time.Second,
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(holder.roundTripNetworkFail)}),
		)
		resp, err := client.executeWithRetry(ctx, "https://www.example.com", "")
		if resp != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Errorf("unexpected close error: %v", cerr)
			}
		}
		if err == nil || !strings.Contains(err.Error(), "context canceled during retry delay") {
			t.Errorf("expected canceled retry delay error, got %v", err)
		}
	})

	t.Run("executeWithRetry abort status close failure", func(t *testing.T) {
		t.Parallel()
		client := NewClient(
			time.Second,
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(mockAbortCloseFailTransport)}),
		)
		resp, err := client.executeWithRetry(context.Background(), "https://www.example.com", "")
		if resp != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Errorf("unexpected close error: %v", cerr)
			}
		}
		if err == nil || !strings.Contains(err.Error(), "close aborted response body") {
			t.Errorf("expected close aborted response body error, got %v", err)
		}
	})

	t.Run("executeWithRetry retry status close failure", func(t *testing.T) {
		t.Parallel()
		client := NewClient(
			time.Second,
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(mockRetryCloseFailTransport)}),
		)
		resp, err := client.executeWithRetry(context.Background(), "https://www.example.com", "")
		if resp != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Errorf("unexpected close error: %v", cerr)
			}
		}
		if err == nil || !strings.Contains(err.Error(), "close retry response body") {
			t.Errorf("expected close retry response body error, got %v", err)
		}
	})
}

func TestClientBackoffErrors(t *testing.T) {
	t.Parallel()

	t.Run("executeWithRetry rate limit 429 backoff", func(t *testing.T) {
		t.Parallel()
		client := NewClient(
			time.Second,
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(mockRateLimitTransport)}),
			WithSleepFunc(mockInstantSleepContext),
		)
		resp, err := client.executeWithRetry(context.Background(), "https://www.example.com", "")
		if resp != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Errorf("unexpected close error: %v", cerr)
			}
		}
		if err == nil || !strings.Contains(err.Error(), "request exceeded max retries") {
			t.Errorf("expected exceeded max retries error, got %v", err)
		}
		if !errors.Is(err, ErrRateLimitExceeded) || !errors.Is(err, ErrUnexpectedStatus) {
			t.Errorf("expected ErrRateLimitExceeded and ErrUnexpectedStatus, got %v", err)
		}
	})

	t.Run("executeWithRetry canceled during backoff", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		holder := &cancelFuncHolder{cancel: cancel}
		client := NewClient(
			time.Second,
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(holder.roundTripRetryFail)}),
		)
		resp, err := client.executeWithRetry(ctx, "https://www.example.com", "")
		if resp != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Errorf("unexpected close error: %v", cerr)
			}
		}
		if err == nil || !strings.Contains(err.Error(), "context canceled during backoff") {
			t.Errorf("expected canceled during backoff error, got %v", err)
		}
	})
}

func TestClientRateLimitAndForbiddenAbort(t *testing.T) {
	t.Parallel()

	t.Run("executeWithRetry rate limit 429 aborts immediately when retry delay exceeds 1m", func(t *testing.T) {
		t.Parallel()
		attempts := 0
		sleepCalled := false
		longWaitTransport := func(_ *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Retry-After": []string{"120"}},
				Body:       io.NopCloser(strings.NewReader("rate limited long")),
			}, nil
		}
		client := NewClient(
			time.Second,
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(longWaitTransport)}),
			WithSleepFunc(func(_ context.Context, _ time.Duration) bool {
				sleepCalled = true
				return true
			}),
		)
		resp, err := client.executeWithRetry(context.Background(), "https://longwait.example.com", "")
		if resp != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Errorf("unexpected close error: %v", cerr)
			}
		}
		if !errors.Is(err, ErrRateLimitExceeded) || !errors.Is(err, ErrUnexpectedStatus) {
			t.Fatalf("expected ErrRateLimitExceeded and ErrUnexpectedStatus, got %v", err)
		}
		if attempts != 1 {
			t.Errorf("expected 1 attempt without retry, got %d", attempts)
		}
		if sleepCalled {
			t.Errorf("expected sleep not to be called when retry delay exceeds limit")
		}
	})

	t.Run("executeWithRetry 403 forbidden returns ErrAccessBlocked immediately", func(t *testing.T) {
		t.Parallel()
		attempts := 0
		forbiddenTransport := func(_ *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader("forbidden")),
			}, nil
		}
		client := NewClient(
			time.Second,
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(forbiddenTransport)}),
		)
		resp, err := client.executeWithRetry(context.Background(), "https://forbidden.example.com", "")
		if resp != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Errorf("unexpected close error: %v", cerr)
			}
		}
		if !errors.Is(err, ErrAccessBlocked) || !errors.Is(err, ErrUnexpectedStatus) {
			t.Fatalf("expected ErrAccessBlocked and ErrUnexpectedStatus, got %v", err)
		}
		if attempts != 1 {
			t.Errorf("expected 1 attempt for 403, got %d", attempts)
		}
	})
}

func TestClientAdditionalEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("FetchSeriesMetadata invalid url", func(t *testing.T) {
		t.Parallel()
		client := NewClient(time.Second)
		_, err := client.FetchSeriesMetadata(context.Background(), "")
		if err == nil {
			t.Errorf("expected error from empty series url, got nil")
		}
	})

	t.Run("FetchChapterImageURLs getHTML error", func(t *testing.T) {
		t.Parallel()
		client := NewClient(
			time.Second,
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(mockFailingChapterViewerTransport)}),
			WithSleepFunc(mockInstantSleepContext),
		)
		_, err := client.FetchChapterImageURLs(context.Background(), "https://www.webtoons.com/viewer")
		if err == nil || !strings.Contains(err.Error(), "fetch chapter viewer") {
			t.Errorf("expected fetch chapter viewer error, got %v", err)
		}
	})

	t.Run("executeWithRetry transient network error then success", func(t *testing.T) {
		t.Parallel()
		holder := &attemptCounterHolder{}
		client := NewClient(
			time.Second,
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(holder.roundTripRetryThenSuccess)}),
			WithSleepFunc(mockInstantSleepContext),
		)
		resp, err := client.executeWithRetry(context.Background(), "https://www.example.com/retry-success", "")
		if err != nil {
			t.Fatalf("expected retry success, got: %v", err)
		}
		if resp != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Errorf("unexpected close error: %v", cerr)
			}
		}
	})

	t.Run("FetchChapters defer body close error", func(t *testing.T) {
		t.Parallel()
		client := NewClient(
			time.Second,
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(mockFailReadAndCloseTransport)}),
		)
		series := &SeriesInfo{
			SeriesType: "challenge",
			TitleNo:    1234,
			Language:   "en",
		}
		_, err := client.FetchChapters(context.Background(), series)
		if err == nil || !strings.Contains(err.Error(), "decode episodes json") {
			t.Errorf("expected decode episodes json error, got: %v", err)
		}
	})

	t.Run("DownloadImage defer body close error", func(t *testing.T) {
		t.Parallel()
		client := NewClient(
			time.Second,
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(mockFailReadAndCloseTransport)}),
		)
		_, err := client.DownloadImage(context.Background(), "https://www.example.com/image.jpg")
		if err == nil || !strings.Contains(err.Error(), "read image body") {
			t.Errorf("expected read image body error, got: %v", err)
		}
	})

	t.Run("getHTML defer body close error", func(t *testing.T) {
		t.Parallel()
		client := NewClient(
			time.Second,
			WithHTTPClient(&http.Client{Transport: mockRoundTripFunc(mockFailReadAndCloseTransport)}),
		)
		_, err := client.getHTML(context.Background(), "https://www.example.com/page.html", "")
		if err == nil || !strings.Contains(err.Error(), "read html response") {
			t.Errorf("expected read html response error, got %v", err)
		}
	})

	t.Run("client sleep fallback to SleepContext", func(t *testing.T) {
		t.Parallel()
		client := NewClient(time.Second)
		if !client.sleep(context.Background(), time.Nanosecond) {
			t.Errorf("expected true from client.sleep with nil sleepFn")
		}
	})
}

func TestParserWhitespaceImageAndViewerBoundary(t *testing.T) {
	t.Parallel()

	t.Run("ExtractImageURLs with whitespace only data url", func(t *testing.T) {
		t.Parallel()
		markup := `<div id="_viewerBox"><img data-url="   " /></div>`
		urls, err := ExtractImageURLs(markup)
		if err == nil || !errors.Is(err, ErrNoImagesFound) {
			t.Errorf("expected ErrNoImagesFound for whitespace-only data-url, got %v (urls: %v)", err, urls)
		}
	})

	t.Run("extractViewerContainer boundary past string length", func(t *testing.T) {
		t.Parallel()
		input := "<div><div>content</div>"
		res := extractViewerContainer(input)
		if res != input {
			t.Errorf("expected full input %q, got %q", input, res)
		}
	})
}

func TestReadBoundedBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		errExpected error
		name        string
		content     string
		maxBytes    int64
	}{
		{
			name:        "within bounds succeeds",
			content:     "small payload",
			maxBytes:    100,
			errExpected: nil,
		},
		{
			name:        "exceeds bounds returns ErrResponseTooLarge",
			content:     "payload exceeding limit",
			maxBytes:    10,
			errExpected: ErrResponseTooLarge,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := strings.NewReader(tc.content)
			data, err := readBoundedBody(r, tc.maxBytes)
			if tc.errExpected != nil {
				if !errors.Is(err, tc.errExpected) {
					t.Fatalf("expected error %v, got %v", tc.errExpected, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(data) != tc.content {
				t.Errorf("expected %q, got %q", tc.content, string(data))
			}
		})
	}
}

func TestURLValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		rawURL     string
		baseURL    string
		expectErr  bool
		testViewer bool
		testImage  bool
	}{
		{
			name:       "valid webtoons viewer url",
			rawURL:     "https://www.webtoons.com/en/fantasy/tower/viewer?episode_no=1",
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "valid mobile webtoons viewer url",
			rawURL:     "https://m.webtoons.com/viewer",
			testViewer: true,
		},
		{
			name:       "valid custom base url matching viewer",
			rawURL:     "https://sub.example.com/viewer",
			baseURL:    "https://sub.example.com",
			testViewer: true,
		},
		{
			name:       "valid custom port matching base url",
			rawURL:     "https://sub.example.com:8443/viewer",
			baseURL:    "https://sub.example.com:8443",
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "viewer rejected for foreign domain not matching base",
			rawURL:     "https://cdn.example.org/viewer",
			expectErr:  true,
			testViewer: true,
		},
		{
			name:      "image allowed on https cdn domain",
			rawURL:    "https://cdn.example.org/page.jpg",
			testImage: true,
		},
		{
			name:       "image rejected for http scheme",
			rawURL:     "http://cdn.example.org/page.jpg",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "rejected for userinfo",
			rawURL:     "https://fakeuser@cdn.example.org/page.jpg",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "rejected for non-443 non-base port",
			rawURL:     "https://cdn.example.org:8080/page.jpg",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "rejected for ipv4 literal",
			rawURL:     "https://127.0.0.1/page.jpg",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "rejected for cloud metadata ipv4 literal",
			rawURL:     "https://169.254.169.254/page.jpg",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "rejected for localhost",
			rawURL:     "https://localhost/page.jpg",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "rejected for internal domain",
			rawURL:     "https://service.internal/page.jpg",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "rejected for malformed url",
			rawURL:     "://invalid-url",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "rejected for empty port colon with default base port",
			rawURL:     "https://cdn.example.org:/page_empty_port.jpg",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "rejected for empty port colon with custom base port",
			rawURL:     "https://sub.example.com:/page_custom_empty.jpg",
			baseURL:    "https://sub.example.com:8443",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "rejected for localhost with trailing dot",
			rawURL:     "https://localhost./page_dot.jpg",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "rejected for internal domain with trailing dot",
			rawURL:     "https://service.internal./page_dot_internal.jpg",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "rejected for non-canonical shorthand ipv4",
			rawURL:     "https://127.1/page_shorthand.jpg",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "rejected for decimal integer ipv4",
			rawURL:     "https://2130706433/page_int.jpg",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:       "rejected for ipv6 zone id in brackets",
			rawURL:     "https://[::1%25lo0]/page_zone.jpg",
			expectErr:  true,
			testViewer: true,
			testImage:  true,
		},
		{
			name:      "accepted for valid domain with trailing dot",
			rawURL:    "https://cdn.example.org./page_valid_dot.jpg",
			testImage: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			baseURL := tc.baseURL
			if baseURL == "" {
				baseURL = defaultWebtoonURL
			}

			if tc.testViewer {
				err := validateViewerURL(tc.rawURL, baseURL)
				if tc.expectErr && err == nil {
					t.Errorf("validateViewerURL expected error for %s, got nil", tc.rawURL)
				}
				if !tc.expectErr && err != nil {
					t.Errorf("validateViewerURL unexpected error for %s: %v", tc.rawURL, err)
				}
			}

			if tc.testImage {
				err := validateImageURL(tc.rawURL, baseURL)
				if tc.expectErr && err == nil {
					t.Errorf("validateImageURL expected error for %s, got nil", tc.rawURL)
				}
				if !tc.expectErr && err != nil {
					t.Errorf("validateImageURL unexpected error for %s: %v", tc.rawURL, err)
				}
			}
		})
	}
}

func TestCheckRedirect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		targetURL string
		viaPort   string
		viaCount  int
		expectErr bool
	}{
		{
			name:      "valid https redirect",
			targetURL: "https://cdn.example.org/image.jpg",
			viaCount:  1,
			expectErr: false,
		},
		{
			name:      "redirect stopped after max",
			targetURL: "https://cdn.example.org/image.jpg",
			viaCount:  10,
			expectErr: true,
		},
		{
			name:      "redirect to http rejected",
			targetURL: "http://cdn.example.org/image.jpg",
			viaCount:  1,
			expectErr: true,
		},
		{
			name:      "redirect to userinfo rejected",
			targetURL: "https://fakeuser@cdn.example.org/image.jpg",
			viaCount:  1,
			expectErr: true,
		},
		{
			name:      "redirect to non-443 port rejected",
			targetURL: "https://cdn.example.org:8080/image.jpg",
			viaCount:  1,
			expectErr: true,
		},
		{
			name:      "redirect to matching via port allowed",
			targetURL: "https://cdn.example.org:8443/image.jpg",
			viaPort:   ":8443",
			viaCount:  1,
			expectErr: false,
		},
		{
			name:      "redirect to localhost rejected",
			targetURL: "https://localhost/image.jpg",
			viaCount:  1,
			expectErr: true,
		},
		{
			name:      "redirect to ip literal rejected",
			targetURL: "https://127.0.0.1/image.jpg",
			viaCount:  1,
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, tc.targetURL, http.NoBody)
			if err != nil {
				t.Fatalf("failed to create req: %v", err)
			}

			via := make([]*http.Request, tc.viaCount)
			viaURL := "https://www.webtoons.com" + tc.viaPort + "/start"
			for i := range tc.viaCount {
				viaReq, viaErr := http.NewRequestWithContext(context.Background(), http.MethodGet, viaURL, http.NoBody)
				if viaErr != nil {
					t.Fatalf("failed to create via req: %v", viaErr)
				}
				via[i] = viaReq
			}

			err = defaultCheckRedirect(req, via)
			if tc.expectErr && err == nil {
				t.Errorf("defaultCheckRedirect expected error for %s, got nil", tc.targetURL)
			}
			if !tc.expectErr && err != nil {
				t.Errorf("defaultCheckRedirect unexpected error for %s: %v", tc.targetURL, err)
			}
		})
	}
}

func TestClientSecurityEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("FetchChapters invalid viewer link rejected", func(t *testing.T) {
		t.Parallel()
		httpClient := &http.Client{
			Transport: mockRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
				body := `{"result":{"episodeList":[{"episodeNo":1,"episodeTitle":"Ep1","viewerLink":"http://insecure.example.com/viewer"}]}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			}),
		}
		client := NewClient(time.Second, WithHTTPClient(httpClient))
		series := &SeriesInfo{SeriesType: "webtoon", TitleNo: 1, Language: "en"}
		_, err := client.FetchChapters(context.Background(), series)
		if err == nil || !strings.Contains(err.Error(), "invalid chapter viewer link") {
			t.Errorf("expected invalid chapter viewer link error, got %v", err)
		}
	})

	t.Run("FetchChapterImageURLs invalid viewer url rejected", func(t *testing.T) {
		t.Parallel()
		client := NewClient(time.Second)
		_, err := client.FetchChapterImageURLs(context.Background(), "http://insecure.example.com/viewer")
		if err == nil || !strings.Contains(err.Error(), "invalid chapter viewer url") {
			t.Errorf("expected invalid chapter viewer url error, got %v", err)
		}
	})

	t.Run("DownloadImage disallowed url rejected", func(t *testing.T) {
		t.Parallel()
		client := NewClient(time.Second)
		_, err := client.DownloadImage(context.Background(), "https://127.0.0.1/image.jpg")
		if err == nil || !strings.Contains(err.Error(), "invalid image url") {
			t.Errorf("expected invalid image url error, got %v", err)
		}
	})

	t.Run("DownloadImage relative url prepends base url", func(t *testing.T) {
		t.Parallel()
		httpClient := &http.Client{
			Transport: mockRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != "https://www.webtoons.com/img/page1.jpg" {
					return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader("bad url"))}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("\xff\xd8\xffrel_data"))}, nil
			}),
		}
		client := NewClient(time.Second, WithHTTPClient(httpClient))
		data, err := client.DownloadImage(context.Background(), "/img/page1.jpg")
		if err != nil {
			t.Fatalf("unexpected error downloading relative image: %v", err)
		}
		if string(data) != "\xff\xd8\xffrel_data" {
			t.Errorf("expected data, got %s", string(data))
		}
	})

	t.Run("DownloadImage protocol relative url prepends https", func(t *testing.T) {
		t.Parallel()
		httpClient := &http.Client{
			Transport: mockRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != "https://cdn.example.org/img/page1.jpg" {
					return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader("bad url"))}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("\xff\xd8\xffproto_data"))}, nil
			}),
		}
		client := NewClient(time.Second, WithHTTPClient(httpClient))
		data, err := client.DownloadImage(context.Background(), "//cdn.example.org/img/page1.jpg")
		if err != nil {
			t.Fatalf("unexpected error downloading protocol-relative image: %v", err)
		}
		if string(data) != "\xff\xd8\xffproto_data" {
			t.Errorf("expected data, got %s", string(data))
		}
	})
}

func TestExtractBasePort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		baseURL  string
		expected string
	}{
		{
			name:     "invalid url returns empty",
			baseURL:  "://invalid",
			expected: "",
		},
		{
			name:     "empty base url returns empty",
			baseURL:  "",
			expected: "",
		},
		{
			name:     "url without port returns empty",
			baseURL:  "https://base.example",
			expected: "",
		},
		{
			name:     "url with port returns port",
			baseURL:  "https://port.example:8443",
			expected: "8443",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractBasePort(tc.baseURL)
			if got != tc.expected {
				t.Errorf("extractBasePort(%q) = %q, expected %q", tc.baseURL, got, tc.expected)
			}
		})
	}
}

func TestIsDisallowedHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		host     string
		expected bool
	}{
		{
			name:     "empty host disallowed",
			host:     "",
			expected: true,
		},
		{
			name:     "subdomain localhost disallowed",
			host:     "sub.localhost",
			expected: true,
		},
		{
			name:     "local suffix disallowed",
			host:     "my.local",
			expected: true,
		},
		{
			name:     "internal suffix disallowed",
			host:     "service.internal",
			expected: true,
		},
		{
			name:     "corp suffix disallowed",
			host:     "intra.corp",
			expected: true,
		},
		{
			name:     "home suffix disallowed",
			host:     "router.home",
			expected: true,
		},
		{
			name:     "lan suffix disallowed",
			host:     "modem.lan",
			expected: true,
		},
		{
			name:     "host with port disallowed ip",
			host:     "127.0.0.1:8080",
			expected: true,
		},
		{
			name:     "ipv6 in brackets disallowed",
			host:     "[::1]",
			expected: true,
		},
		{
			name:     "regular domain allowed",
			host:     "domain.example",
			expected: false,
		},
		{
			name:     "regular domain with port allowed",
			host:     "host.example:443",
			expected: false,
		},
		{
			name:     "localhost with trailing dot disallowed",
			host:     "localhost.",
			expected: true,
		},
		{
			name:     "internal suffix with trailing dot disallowed",
			host:     "metadata.internal.",
			expected: true,
		},
		{
			name:     "local suffix with trailing dot disallowed",
			host:     "server.local.",
			expected: true,
		},
		{
			name:     "dots only disallowed",
			host:     "....",
			expected: true,
		},
		{
			name:     "ipv6 with zone id in brackets disallowed",
			host:     "[::1%25lo0]",
			expected: true,
		},
		{
			name:     "ipv6 with zone id and port disallowed",
			host:     "[::1%25eth0]:443",
			expected: true,
		},
		{
			name:     "ipv6 with zone id unbracketed disallowed",
			host:     "::1%25lo0",
			expected: true,
		},
		{
			name:     "ipv6 with colon disallowed",
			host:     "fe80::1",
			expected: true,
		},
		{
			name:     "shorthand ipv4 disallowed",
			host:     "127.1",
			expected: true,
		},
		{
			name:     "integer ipv4 disallowed",
			host:     "2130706433",
			expected: true,
		},
		{
			name:     "octal ipv4 disallowed",
			host:     "0177.0.0.1",
			expected: true,
		},
		{
			name:     "hex ipv4 disallowed",
			host:     "0x7f000001",
			expected: true,
		},
		{
			name:     "hex prefix with invalid chars not numeric",
			host:     "0xnothex",
			expected: false,
		},
		{
			name:     "domain with trailing dot allowed",
			host:     "zone.example.",
			expected: false,
		},
		{
			name:     "domain with 0x prefix allowed",
			host:     "0xzone.example",
			expected: false,
		},
		{
			name:     "subdomain with numbers allowed",
			host:     "cdn123.example.com",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isDisallowedHost(tc.host)
			if got != tc.expected {
				t.Errorf("isDisallowedHost(%q) = %v, expected %v", tc.host, got, tc.expected)
			}
		})
	}
}

func TestMatchesBaseHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rawHost  string
		baseURL  string
		expected bool
	}{
		{
			name:     "invalid base url returns false",
			rawHost:  "invalid.example",
			baseURL:  "://invalid",
			expected: false,
		},
		{
			name:     "empty base url returns false",
			rawHost:  "empty.example",
			baseURL:  "",
			expected: false,
		},
		{
			name:     "raw host with port matches base without port",
			rawHost:  "match.example:8443",
			baseURL:  "https://match.example",
			expected: true,
		},
		{
			name:     "raw host without port matches base with port",
			rawHost:  "target.example",
			baseURL:  "https://target.example:8443",
			expected: true,
		},
		{
			name:     "different host returns false",
			rawHost:  "mismatch.example",
			baseURL:  "https://origin.example",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := matchesBaseHost(tc.rawHost, tc.baseURL)
			if got != tc.expected {
				t.Errorf("matchesBaseHost(%q, %q) = %v, expected %v", tc.rawHost, tc.baseURL, got, tc.expected)
			}
		})
	}
}
