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

func TestParseRateLimitDelayTable(t *testing.T) {
	t.Parallel()

	futureTime := time.Now().Add(10 * time.Second)
	pastTime := time.Now().Add(-10 * time.Second)
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
			t.Parallel()

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
			{name: "disallowed domain", host: "google.com", expected: false},
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
			withSleepFunc(mockInstantSleepContext),
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
			withSleepFunc(mockInstantSleepContext),
		)
		_, err := client.FetchChapterImageURLs(context.Background(), "https://www.example.com/viewer")
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
			withSleepFunc(mockInstantSleepContext),
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
