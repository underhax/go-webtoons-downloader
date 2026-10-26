package webtoon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultWebtoonURL        = "https://www.webtoons.com"
	defaultMobileWebtoonURL  = "https://m.webtoons.com"
	defaultUserAgent         = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	maxMetadataResponseBytes = 10 << 20
	maxImageResponseBytes    = 50 << 20
	defaultMaxRedirects      = 10
)

// Sentinels returned when Webtoon API or network interactions fail.
var (
	ErrUnexpectedStatus  = errors.New("unexpected http status")
	ErrAccessBlocked     = errors.New("access blocked by server")
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
	ErrNoEpisodes        = errors.New("no episodes returned for series")
	ErrResponseTooLarge  = errors.New("response body exceeds size limit")
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
		if c.httpClient != nil && c.httpClient.CheckRedirect == nil {
			c.httpClient.CheckRedirect = defaultCheckRedirect
		}
	}
}

// WithSleepFunc overrides the sleep function used between retry attempts.
func WithSleepFunc(fn func(context.Context, time.Duration) bool) ClientOption {
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
			Timeout:       timeout,
			CheckRedirect: defaultCheckRedirect,
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

	canonicalURL := fmt.Sprintf("%s/%s/%s/%s/list?title_no=%d", c.baseURL, info.Language, info.Genre, info.TitleSlug, info.TitleNo)

	body, err := c.getHTML(ctx, canonicalURL, c.baseURL)
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

	bodyBytes, err := readBoundedBody(resp.Body, maxMetadataResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("decode episodes json: %w", err)
	}

	var payload episodesResponse
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return nil, fmt.Errorf("decode episodes json: %w", err)
	}

	items := payload.Result.EpisodeList
	if len(items) == 0 {
		return nil, ErrNoEpisodes
	}

	chapters := make([]Chapter, 0, len(items))
	for i, item := range items {
		viewerURL := strings.TrimSpace(item.ViewerLink)
		if !strings.HasPrefix(viewerURL, "http://") && !strings.HasPrefix(viewerURL, "https://") {
			viewerURL = c.baseURL + "/" + strings.TrimLeft(viewerURL, "/")
		}

		if err := validateViewerURL(viewerURL, c.baseURL); err != nil {
			return nil, fmt.Errorf("invalid chapter viewer link: %w", err)
		}

		chapters = append(chapters, Chapter{
			Number:    i + 1,
			EpisodeNo: item.EpisodeNo,
			Title:     cleanMetadataText(item.EpisodeTitle),
			ViewerURL: viewerURL,
		})
	}

	return chapters, nil
}

// FetchChapterImageURLs extracts ordered image URLs from the chapter viewer DOM.
func (c *Client) FetchChapterImageURLs(ctx context.Context, viewerURL string) ([]string, error) {
	if err := validateViewerURL(viewerURL, c.baseURL); err != nil {
		return nil, fmt.Errorf("invalid chapter viewer url: %w", err)
	}

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
	reqURL := strings.TrimSpace(imageURL)
	if after, ok := strings.CutPrefix(reqURL, "//"); ok {
		reqURL = "https://" + after
	} else if strings.HasPrefix(reqURL, "/") {
		reqURL = c.baseURL + reqURL
	}

	if err := validateImageURL(reqURL, c.baseURL); err != nil {
		return nil, fmt.Errorf("invalid image url: %w", err)
	}

	resp, err := c.executeWithRetry(ctx, reqURL, c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("download image: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && !errors.Is(cerr, http.ErrBodyReadAfterClose) {
			return
		}
	}()

	data, err := readBoundedBody(resp.Body, maxImageResponseBytes)
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

	data, err := readBoundedBody(resp.Body, maxMetadataResponseBytes)
	if err != nil {
		return "", fmt.Errorf("read html response: %w", err)
	}

	return string(data), nil
}

func readBoundedBody(r io.Reader, maxBytes int64) ([]byte, error) {
	lr := io.LimitReader(r, maxBytes+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrResponseTooLarge
	}
	return data, nil
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

		delay, retryErr := handleNonOKResponse(resp, attempt, targetURL)
		if retryErr != nil {
			return nil, retryErr
		}
		lastErr = formatStatusError(resp.StatusCode, targetURL)
		if !c.sleep(ctx, delay) {
			return nil, fmt.Errorf("context canceled during backoff: %w", ctx.Err())
		}
	}

	return nil, fmt.Errorf("request exceeded max retries: %w", lastErr)
}

func handleNonOKResponse(resp *http.Response, attempt int, targetURL string) (time.Duration, error) {
	action := ClassifyStatus(resp.StatusCode)
	if action == ActionAbort {
		if cerr := resp.Body.Close(); cerr != nil && !errors.Is(cerr, http.ErrBodyReadAfterClose) {
			return 0, fmt.Errorf("close aborted response body: %w", cerr)
		}
		return 0, formatStatusError(resp.StatusCode, targetURL)
	}

	delay := RetryDelay(action, attempt, defaultBaseRetryDelay)
	if action == ActionRateLimit {
		delay = parseRateLimitDelay(resp, delay)
	}

	if cerr := resp.Body.Close(); cerr != nil && !errors.Is(cerr, http.ErrBodyReadAfterClose) {
		return 0, fmt.Errorf("close retry response body: %w", cerr)
	}
	if action == ActionRateLimit && delay > maxRateLimitWait {
		return 0, fmt.Errorf("%w: %w: retry delay %v exceeds %v limit for %s", ErrRateLimitExceeded, ErrUnexpectedStatus, delay, maxRateLimitWait, targetURL)
	}
	return delay, nil
}

func formatStatusError(statusCode int, targetURL string) error {
	switch statusCode {
	case http.StatusForbidden:
		return fmt.Errorf("%w: %w: status %d for %s", ErrAccessBlocked, ErrUnexpectedStatus, statusCode, targetURL)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: %w: status %d for %s", ErrRateLimitExceeded, ErrUnexpectedStatus, statusCode, targetURL)
	default:
		return fmt.Errorf("%w: status %d for %s", ErrUnexpectedStatus, statusCode, targetURL)
	}
}

func defaultCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= defaultMaxRedirects {
		return errors.New("stopped after 10 redirects")
	}

	allowedPort := "443"
	if len(via) > 0 && via[0].URL.Port() != "" {
		allowedPort = via[0].URL.Port()
	}

	_, err := validateSafeHTTPS(req.URL, allowedPort)
	if err != nil {
		return fmt.Errorf("%w: redirect failed: %w", ErrInvalidURL, err)
	}

	return nil
}

func validateViewerURL(rawURL, baseURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: invalid viewer url", ErrInvalidURL)
	}

	allowedPort := extractBasePort(baseURL)
	host, err := validateSafeHTTPS(u, allowedPort)
	if err != nil {
		return err
	}

	if !isAllowedWebtoonHost(host) && !matchesBaseHost(host, baseURL) {
		return fmt.Errorf("%w: invalid viewer host %s", ErrInvalidURL, host)
	}

	return nil
}

func validateImageURL(rawURL, baseURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: invalid image url", ErrInvalidURL)
	}

	allowedPort := extractBasePort(baseURL)
	_, err = validateSafeHTTPS(u, allowedPort)
	return err
}

func validateSafeHTTPS(u *url.URL, allowedPort string) (string, error) {
	if u.Scheme != "https" {
		return "", fmt.Errorf("%w: unsupported scheme %s", ErrInvalidURL, u.Scheme)
	}

	if u.User != nil {
		return "", fmt.Errorf("%w: userinfo not allowed", ErrInvalidURL)
	}

	host := u.Host
	if h, port, err := net.SplitHostPort(host); err == nil {
		if port != "443" && (allowedPort == "" || port != allowedPort) {
			return "", fmt.Errorf("%w: non-standard port %s", ErrInvalidURL, port)
		}
		host = h
	}

	if isDisallowedHost(host) {
		return "", fmt.Errorf("%w: disallowed host %s", ErrInvalidURL, host)
	}

	return strings.TrimRight(host, "."), nil
}

func extractBasePort(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return ""
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return ""
	}
	return port
}

func isDisallowedHost(host string) bool {
	if strings.ContainsAny(host, "[]%") {
		return true
	}

	h := host
	if hostPart, _, err := net.SplitHostPort(host); err == nil {
		h = hostPart
	}

	if strings.ContainsAny(h, "[]%:") {
		return true
	}

	h = strings.TrimRight(h, ".")
	if h == "" {
		return true
	}

	hLower := strings.ToLower(h)
	if hLower == "localhost" ||
		strings.HasSuffix(hLower, ".localhost") ||
		strings.HasSuffix(hLower, ".local") ||
		strings.HasSuffix(hLower, ".internal") ||
		strings.HasSuffix(hLower, ".corp") ||
		strings.HasSuffix(hLower, ".home") ||
		strings.HasSuffix(hLower, ".lan") {
		return true
	}

	if isNumericTLD(hLower) {
		return true
	}

	return net.ParseIP(h) != nil
}

func isNumericTLD(host string) bool {
	_, tld, ok := strings.CutLast(host, ".")
	if !ok {
		tld = host
	}

	allDigits := true
	for i := range len(tld) {
		if tld[i] < '0' || tld[i] > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return true
	}

	if len(tld) >= 3 && strings.HasPrefix(tld, "0x") {
		allHex := true
		for _, c := range tld[2:] {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				allHex = false
				break
			}
		}
		if allHex {
			return true
		}
	}

	return false
}

func matchesBaseHost(rawHost, baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return false
	}
	baseHost := strings.TrimRight(strings.ToLower(u.Host), ".")
	if h, _, err := net.SplitHostPort(baseHost); err == nil {
		baseHost = strings.TrimRight(h, ".")
	}

	h := strings.TrimRight(strings.ToLower(rawHost), ".")
	if hostPart, _, err := net.SplitHostPort(rawHost); err == nil {
		h = strings.TrimRight(hostPart, ".")
	}

	return h == baseHost
}
