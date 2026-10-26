package webtoon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultWebtoonURL       = "https://www.webtoons.com"
	defaultMobileWebtoonURL = "https://m.webtoons.com"
	defaultUserAgent        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

var (
	// ErrUnexpectedStatus indicates a non-200 HTTP response.
	ErrUnexpectedStatus = errors.New("unexpected http status")

	// ErrNoEpisodes indicates that the series episode list was empty.
	ErrNoEpisodes = errors.New("no episodes returned for series")
)

// Client handles network interactions with Webtoons web and API endpoints.
type Client struct {
	httpClient     *http.Client
	sleepFn        func(context.Context, time.Duration) bool
	baseURL        string
	mobileBaseURL  string
	userAgent      string
	requestTimeout time.Duration
}

// ClientOption defines functional configuration for the Webtoon Client.
type ClientOption func(*Client)

// WithBaseURLs overrides base endpoints, primarily used for testing against mock servers.
func WithBaseURLs(webtoonURL, mobileURL string) ClientOption {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(webtoonURL, "/")
		c.mobileBaseURL = strings.TrimRight(mobileURL, "/")
	}
}

// WithHTTPClient overrides the default HTTP transport client.
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

func withSleepFunc(fn func(context.Context, time.Duration) bool) ClientOption {
	return func(c *Client) {
		c.sleepFn = fn
	}
}

func (c *Client) sleep(ctx context.Context, d time.Duration) bool {
	if c.sleepFn != nil {
		return c.sleepFn(ctx, d)
	}
	return SleepContext(ctx, d)
}

// NewClient constructs a Webtoon Client with sensible network defaults.
func NewClient(timeout time.Duration, opts ...ClientOption) *Client {
	c := &Client{
		httpClient: &http.Client{
			Timeout: timeout,
		},
		baseURL:        defaultWebtoonURL,
		mobileBaseURL:  defaultMobileWebtoonURL,
		userAgent:      defaultUserAgent,
		requestTimeout: timeout,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// FetchSeriesMetadata resolves full title name and title_no from URL and HTML canonical headers.
func (c *Client) FetchSeriesMetadata(ctx context.Context, rawURL string) (*SeriesInfo, error) {
	info, err := ParseSeriesURL(rawURL)
	if err != nil {
		return nil, err
	}

	reqURL := rawURL
	if !strings.HasPrefix(reqURL, "http://") && !strings.HasPrefix(reqURL, "https://") {
		reqURL = "https://" + strings.TrimSpace(rawURL)
	}

	body, err := c.getHTML(ctx, reqURL, c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("fetch series page: %w", err)
	}

	info.TitleName = ExtractSeriesTitle(body, info.TitleSlug)
	info.Genre = ExtractGenre(body)
	info.Author = ExtractAuthor(body)
	info.Summary = ExtractSummary(body)
	return info, nil
}

type episodesResponse struct {
	Result struct {
		EpisodeList []struct {
			ViewerLink   string `json:"viewerLink"`
			EpisodeTitle string `json:"episodeTitle"`
			EpisodeNo    int    `json:"episodeNo"`
		} `json:"episodeList"`
	} `json:"result"`
}

// FetchChapters queries the mobile API to retrieve all available chapter descriptors.
func (c *Client) FetchChapters(ctx context.Context, series *SeriesInfo) ([]Chapter, error) {
	apiURL := fmt.Sprintf(
		"%s/api/v1/%s/%d/episodes?pageSize=99999&readingLanguageCode=%s",
		c.mobileBaseURL, series.SeriesType, series.TitleNo, series.Language,
	)

	resp, err := c.executeWithRetry(ctx, apiURL, c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("execute episodes request: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && !errors.Is(cerr, http.ErrBodyReadAfterClose) {
			return
		}
	}()

	var payload episodesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode episodes json: %w", err)
	}

	items := payload.Result.EpisodeList
	if len(items) == 0 {
		return nil, ErrNoEpisodes
	}

	chapters := make([]Chapter, 0, len(items))
	for i, item := range items {
		viewerURL := item.ViewerLink
		if !strings.HasPrefix(viewerURL, "http://") && !strings.HasPrefix(viewerURL, "https://") {
			viewerURL = c.baseURL + "/" + strings.TrimLeft(item.ViewerLink, "/")
		}

		chapters = append(chapters, Chapter{
			Number:    i + 1,
			EpisodeNo: item.EpisodeNo,
			Title:     strings.TrimSpace(item.EpisodeTitle),
			ViewerURL: viewerURL,
		})
	}

	return chapters, nil
}

// FetchChapterImageURLs extracts ordered image URLs from the chapter viewer DOM.
func (c *Client) FetchChapterImageURLs(ctx context.Context, viewerURL string) ([]string, error) {
	body, err := c.getHTML(ctx, viewerURL, c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("fetch chapter viewer: %w", err)
	}

	urls, err := ExtractImageURLs(body)
	if err != nil {
		return nil, fmt.Errorf("extract viewer images: %w", err)
	}

	return urls, nil
}

// DownloadImage downloads the binary bytes of a single page image.
func (c *Client) DownloadImage(ctx context.Context, imageURL string) ([]byte, error) {
	resp, err := c.executeWithRetry(ctx, imageURL, c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("download image: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && !errors.Is(cerr, http.ErrBodyReadAfterClose) {
			return
		}
	}()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read image body: %w", err)
	}

	return data, nil
}

func (c *Client) getHTML(ctx context.Context, targetURL, referer string) (string, error) {
	resp, err := c.executeWithRetry(ctx, targetURL, referer)
	if err != nil {
		return "", fmt.Errorf("execute get request: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && !errors.Is(cerr, http.ErrBodyReadAfterClose) {
			return
		}
	}()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read html response: %w", err)
	}

	return string(data), nil
}

func (c *Client) executeWithRetry(ctx context.Context, targetURL, referer string) (*http.Response, error) {
	var lastErr error

	for attempt := range defaultMaxRetries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context canceled before request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, http.NoBody)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		req.Header.Set("User-Agent", c.userAgent)
		if referer != "" {
			req.Header.Set("Referer", referer)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if !c.sleep(ctx, defaultBaseRetryDelay) {
				return nil, fmt.Errorf("context canceled during retry delay: %w", ctx.Err())
			}
			continue
		}

		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}

		action := ClassifyStatus(resp.StatusCode)
		if action == ActionAbort {
			if cerr := resp.Body.Close(); cerr != nil && !errors.Is(cerr, http.ErrBodyReadAfterClose) {
				return nil, fmt.Errorf("close aborted response body: %w", cerr)
			}
			return nil, fmt.Errorf("%w: status %d for %s", ErrUnexpectedStatus, resp.StatusCode, targetURL)
		}

		delay := RetryDelay(action, attempt, defaultBaseRetryDelay)
		if action == ActionRateLimit {
			delay = parseRateLimitDelay(resp, delay)
		}

		if cerr := resp.Body.Close(); cerr != nil && !errors.Is(cerr, http.ErrBodyReadAfterClose) {
			return nil, fmt.Errorf("close retry response body: %w", cerr)
		}
		lastErr = fmt.Errorf("%w: status %d for %s", ErrUnexpectedStatus, resp.StatusCode, targetURL)
		if !c.sleep(ctx, delay) {
			return nil, fmt.Errorf("context canceled during backoff: %w", ctx.Err())
		}
	}

	return nil, fmt.Errorf("request exceeded max retries: %w", lastErr)
}
