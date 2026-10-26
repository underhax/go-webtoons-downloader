package syncer_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/underhax/go-webtoons-downloader/internal/config"
	"github.com/underhax/go-webtoons-downloader/internal/syncer"
	"github.com/underhax/go-webtoons-downloader/internal/webtoon"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func mockSyncerTransport(req *http.Request) (*http.Response, error) {
	path := req.URL.Path

	switch path {
	case "/en/fantasy/tower/list", "/en/fantasy/castle/list", "/en/fantasy/palace/list":
		body := `<html><head><link rel="canonical" href="https://www.webtoons.com/en/fantasy/tower/list?title_no=95"><meta property="og:title" content="Tower of God"></head><body></body></html>`
		return createHTTPResponse(http.StatusOK, "text/html", body), nil

	case "/api/v1/webtoon/95/episodes":
		body := `{"result":{"episodeList":[{"episodeNo":1,"episodeTitle":"Episode 1","viewerLink":"/en/fantasy/tower/viewer?episode_no=1"},{"episodeNo":2,"episodeTitle":"Episode 2","viewerLink":"/en/fantasy/tower/viewer?episode_no=2"}]}}`
		return createHTTPResponse(http.StatusOK, "application/json", body), nil

	case "/en/fantasy/hero/list":
		body := `<html><head><link rel="canonical" href="https://www.webtoons.com/en/fantasy/hero/list?title_no=96"><meta property="og:title" content="Hero"></head><body></body></html>`
		return createHTTPResponse(http.StatusOK, "text/html", body), nil

	case "/api/v1/webtoon/96/episodes":
		body := `{"result":{"episodeList":[{"episodeNo":1,"episodeTitle":"Episode 1","viewerLink":"/en/fantasy/tower/viewer?episode_no=1"},{"episodeNo":2,"episodeTitle":"Episode 2","viewerLink":"/en/fantasy/tower/viewer?episode_no=2"},{"episodeNo":3,"episodeTitle":"Episode 3","viewerLink":"/en/fantasy/tower/viewer?episode_no=3"},{"episodeNo":4,"episodeTitle":"Episode 4","viewerLink":"/en/fantasy/tower/viewer?episode_no=4"}]}}`
		return createHTTPResponse(http.StatusOK, "application/json", body), nil

	case "/en/broken/slug/list":
		return createHTTPResponse(http.StatusInternalServerError, "text/plain", "broken server"), nil

	case "/en/forbidden/slug/list":
		return createHTTPResponse(http.StatusForbidden, "text/plain", "forbidden by waf"), nil

	case "/en/ratelimited/slug/list":
		resp := createHTTPResponse(http.StatusTooManyRequests, "text/plain", "too many requests")
		resp.Header.Set("Retry-After", "120")
		return resp, nil

	case "/en/fantasy/tower/viewer":
		body := `<div class="viewer_img _img_viewer_area"><img data-url="https://www.webtoons.com/img/p1.jpg"></div>`
		return createHTTPResponse(http.StatusOK, "text/html", body), nil

	case "/img/p1.jpg":
		jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xC0, 0x00, 0x11, 0x08, 0x00, 0x64, 0x00, 0x64, 0x03, 0x01, 0x11, 0x00, 0x02, 0x11, 0x01, 0x03, 0x11, 0x01}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"image/jpeg"}},
			Body:       io.NopCloser(bytes.NewReader(jpegHeader)),
		}, nil

	case "/en/broken/list":
		return createHTTPResponse(http.StatusNotFound, "text/plain", "not found"), nil

	default:
		return createHTTPResponse(http.StatusNotFound, "text/plain", "not found"), nil
	}
}

func createHTTPResponse(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type": []string{contentType},
		},
		Body: io.NopCloser(bytes.NewBufferString(body)),
	}
}

func mockInstantSleep(ctx context.Context, _ time.Duration) bool {
	return ctx.Err() == nil
}

func TestSyncerBatchAndDelta(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	linksFile := filepath.Join(tempDir, "links.txt")
	content := "https://www.webtoons.com/en/fantasy/tower/list?title_no=95\n"
	if err := os.WriteFile(linksFile, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write links.txt: %v", err)
	}

	httpClient := &http.Client{
		Transport: roundTripFunc(mockSyncerTransport),
	}

	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
		webtoon.WithHTTPClient(httpClient),
		webtoon.WithSleepFunc(mockInstantSleep),
	)

	cfg := &config.Config{
		Directory:   tempDir,
		LinksFile:   linksFile,
		Format:      config.FormatPDF,
		Concurrency: 2,
		Delay:       0,
		Timeout:     5 * time.Second,
		SeriesJSON:  true,
	}

	var logBuf bytes.Buffer
	syncInstance, err := syncer.New(cfg, client, &logBuf)
	if err != nil {
		t.Fatalf("unexpected error creating syncer: %v", err)
	}

	ctx := context.Background()

	if err := syncInstance.Run(ctx); err != nil {
		t.Fatalf("unexpected error during initial sync: %v", err)
	}

	seriesFolder := filepath.Join(tempDir, "001. tower")
	ch1 := filepath.Join(seriesFolder, "001.pdf")
	ch2 := filepath.Join(seriesFolder, "002.pdf")

	if _, err := os.Stat(ch1); err != nil {
		t.Errorf("expected chapter 1 to exist: %v", err)
	}
	if _, err := os.Stat(ch2); err != nil {
		t.Errorf("expected chapter 2 to exist: %v", err)
	}

	seriesJSON := filepath.Join(seriesFolder, "series.json")
	if _, err := os.Stat(seriesJSON); err != nil {
		t.Errorf("expected series.json to exist: %v", err)
	}

	logBuf.Reset()
	if err := syncInstance.Run(ctx); err != nil {
		t.Fatalf("unexpected error during second sync: %v", err)
	}

	if !bytes.Contains(logBuf.Bytes(), []byte("[UP-TO-DATE]")) {
		t.Errorf("expected [UP-TO-DATE] in logs, got: %s", logBuf.String())
	}
}

func TestSyncerSingleURLAndIndexNotFound(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	httpClient := &http.Client{
		Transport: roundTripFunc(mockSyncerTransport),
	}

	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
		webtoon.WithHTTPClient(httpClient),
		webtoon.WithSleepFunc(mockInstantSleep),
	)

	cfg := &config.Config{
		Directory:   tempDir,
		URL:         "https://www.webtoons.com/en/fantasy/castle/list?title_no=95",
		Format:      config.FormatCBZ,
		Concurrency: 1,
		Delay:       0,
		Timeout:     5 * time.Second,
	}

	var logBuf bytes.Buffer
	syncInstance, err := syncer.New(cfg, client, &logBuf)
	if err != nil {
		t.Fatalf("unexpected error creating syncer: %v", err)
	}

	ctx := context.Background()
	if syncErr := syncInstance.Run(ctx); syncErr != nil {
		t.Fatalf("unexpected error syncing single url: %v", syncErr)
	}

	seriesFolder := filepath.Join(tempDir, "castle")
	ch1 := filepath.Join(seriesFolder, "001.cbz")
	if _, statErr := os.Stat(ch1); statErr != nil {
		t.Errorf("expected chapter 1 cbz to exist: %v", statErr)
	}

	seriesJSON := filepath.Join(seriesFolder, "series.json")
	if _, statErr := os.Stat(seriesJSON); statErr == nil {
		t.Errorf("expected series.json not to exist when SeriesJSON is disabled")
	}

	linksFile := filepath.Join(tempDir, "empty_links.txt")
	if writeErr := os.WriteFile(linksFile, []byte("https://www.webtoons.com/en/fantasy/lore/list?title_no=95\n"), 0o600); writeErr != nil {
		t.Fatalf("failed to write empty_links: %v", writeErr)
	}

	missingIndexCfg := &config.Config{
		Directory:   tempDir,
		LinksFile:   linksFile,
		Format:      config.FormatPDF,
		Index:       99,
		Concurrency: 1,
		Timeout:     5 * time.Second,
	}

	missingSync, err := syncer.New(missingIndexCfg, client, &logBuf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = missingSync.Run(ctx)
	if !errors.Is(err, syncer.ErrTargetIndexNotFound) {
		t.Errorf("expected ErrTargetIndexNotFound, got %v", err)
	}
}

func TestSyncerFailureReporting(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	linksFile := filepath.Join(tempDir, "broken_links.txt")
	content := "https://www.webtoons.com/en/broken/slug/list?title_no=9999\nhttps://www.webtoons.com/en/fantasy/tower/list?title_no=95\n"
	if err := os.WriteFile(linksFile, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write broken_links: %v", err)
	}

	httpClient := &http.Client{
		Transport: roundTripFunc(mockSyncerTransport),
	}

	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
		webtoon.WithHTTPClient(httpClient),
		webtoon.WithSleepFunc(mockInstantSleep),
	)

	cfg := &config.Config{
		Directory:   tempDir,
		LinksFile:   linksFile,
		Format:      config.FormatPDF,
		Concurrency: 1,
		Timeout:     5 * time.Second,
	}

	var logBuf bytes.Buffer
	syncInstance, err := syncer.New(cfg, client, &logBuf)
	if err != nil {
		t.Fatalf("unexpected error creating syncer: %v", err)
	}

	ctx := context.Background()
	err = syncInstance.Run(ctx)
	if !errors.Is(err, syncer.ErrSyncFailure) {
		t.Fatalf("expected ErrSyncFailure, got %v", err)
	}

	if !bytes.Contains(logBuf.Bytes(), []byte("[ERROR] Title 1")) {
		t.Errorf("expected error log for title 1, got: %s", logBuf.String())
	}

	seriesFolder := filepath.Join(tempDir, "002. tower")
	ch1 := filepath.Join(seriesFolder, "001.pdf")
	if _, statErr := os.Stat(ch1); statErr != nil {
		t.Errorf("expected successful title 2 to be downloaded despite title 1 failure: %v", statErr)
	}
}

func TestSyncerFatalErrorsAbortBatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expectCause error
		name        string
		firstLink   string
	}{
		{
			expectCause: webtoon.ErrAccessBlocked,
			name:        "403 forbidden aborts remaining links",
			firstLink:   "https://www.webtoons.com/en/forbidden/slug/list?title_no=9998",
		},
		{
			expectCause: webtoon.ErrRateLimitExceeded,
			name:        "429 long retry after aborts remaining links",
			firstLink:   "https://www.webtoons.com/en/ratelimited/slug/list?title_no=9997",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			linksFile := filepath.Join(tempDir, "fatal_links.txt")
			content := tc.firstLink + "\nhttps://www.webtoons.com/en/fantasy/tower/list?title_no=95\n"
			if err := os.WriteFile(linksFile, []byte(content), 0o600); err != nil {
				t.Fatalf("failed to write fatal_links: %v", err)
			}

			client := webtoon.NewClient(
				5*time.Second,
				webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
				webtoon.WithHTTPClient(&http.Client{Transport: roundTripFunc(mockSyncerTransport)}),
			)

			cfg := &config.Config{
				Directory:   tempDir,
				LinksFile:   linksFile,
				Format:      config.FormatPDF,
				Concurrency: 1,
				Timeout:     5 * time.Second,
			}

			var logBuf bytes.Buffer
			syncInstance, err := syncer.New(cfg, client, &logBuf)
			if err != nil {
				t.Fatalf("unexpected error creating syncer: %v", err)
			}

			err = syncInstance.Run(context.Background())
			if !errors.Is(err, syncer.ErrSyncFailure) || !errors.Is(err, tc.expectCause) {
				t.Fatalf("expected ErrSyncFailure wrapping %v, got %v", tc.expectCause, err)
			}

			secondTitleFolder := filepath.Join(tempDir, "002. tower")
			if _, statErr := os.Stat(secondTitleFolder); statErr == nil {
				t.Errorf("expected title 2 not to be processed after fatal error on title 1")
			}
		})
	}
}

type reportTestCase struct {
	name             string
	expectedLogs     []string
	legacyFiles      []string
	existingEpisodes []int
	start            int
	titleNo          int
	expectUpToDate   bool
}

func TestSyncerSkipAndDeltaReporting(t *testing.T) {
	t.Parallel()

	tests := []reportTestCase{
		{
			name:             "single chapter skipped and subsequent downloaded",
			titleNo:          95,
			start:            1,
			existingEpisodes: []int{1},
			expectedLogs: []string{
				"[SERIES COMPLETED] Tower of God: 1 of 2 chapters downloaded (skipped chapters: 1)",
			},
			expectUpToDate: false,
		},
		{
			name:             "single requested chapter already present",
			titleNo:          95,
			start:            2,
			existingEpisodes: []int{2},
			expectedLogs: []string{
				"[UP-TO-DATE] Tower of God: chapter 2 already present",
			},
			expectUpToDate: true,
		},
		{
			name:             "multiple requested chapters already present",
			titleNo:          95,
			start:            1,
			existingEpisodes: []int{1, 2},
			expectedLogs: []string{
				"[UP-TO-DATE] Tower of God: chapters 1, 2 already present",
			},
			expectUpToDate: true,
		},
		{
			name:           "start chapter exceeds total available chapters",
			titleNo:        95,
			start:          10,
			expectUpToDate: true,
			expectedLogs: []string{
				"[UP-TO-DATE] Tower of God: start chapter 10 exceeds available chapters (2)",
			},
		},
		{
			name:        "legacy unpadded chapter file recognized and skipped",
			titleNo:     95,
			start:       1,
			legacyFiles: []string{"1.pdf"},
			expectedLogs: []string{
				"[SERIES COMPLETED] Tower of God: 1 of 2 chapters downloaded (skipped chapters: 1)",
			},
			expectUpToDate: false,
		},
		{
			name:             "multiple chapters downloaded with one skipped",
			titleNo:          96,
			start:            1,
			existingEpisodes: []int{2},
			expectedLogs: []string{
				"[SERIES COMPLETED] Hero: 3 of 4 chapters downloaded (skipped chapters: 2)",
			},
			expectUpToDate: false,
		},
		{
			name:             "multiple chapters downloaded with multiple skipped",
			titleNo:          96,
			start:            1,
			existingEpisodes: []int{3, 4},
			expectedLogs: []string{
				"[SERIES COMPLETED] Hero: 2 of 4 chapters downloaded (skipped chapters: 3, 4)",
			},
			expectUpToDate: false,
		},
		{
			name:             "single chapter downloaded with multiple skipped",
			titleNo:          96,
			start:            1,
			existingEpisodes: []int{1, 2, 4},
			expectedLogs: []string{
				"[SERIES COMPLETED] Hero: 1 of 4 chapters downloaded (skipped chapters: 1, 2, 4)",
			},
			expectUpToDate: false,
		},
		{
			name:             "consecutive skipped chapters collapsed to range",
			titleNo:          96,
			start:            1,
			existingEpisodes: []int{1, 2, 3},
			expectedLogs: []string{
				"[SERIES COMPLETED] Hero: 1 of 4 chapters downloaded (skipped chapters: 1-3)",
			},
			expectUpToDate: false,
		},
		{
			name:           "all chapters downloaded with no skips",
			titleNo:        96,
			start:          1,
			expectUpToDate: false,
			expectedLogs: []string{
				"[SERIES COMPLETED] Hero: all 4 chapters downloaded",
			},
		},
		{
			name:           "downloaded subset single chapter starting above 1 without skips",
			titleNo:        96,
			start:          4,
			expectUpToDate: false,
			expectedLogs: []string{
				"[SERIES COMPLETED] Hero: 1 chapter downloaded",
			},
		},
		{
			name:           "downloaded subset multiple chapters starting above 1 without skips",
			titleNo:        96,
			start:          2,
			expectUpToDate: false,
			expectedLogs: []string{
				"[SERIES COMPLETED] Hero: 3 chapters downloaded",
			},
		},
		{
			name:             "up to date when start is zero and existing is latest",
			titleNo:          96,
			start:            0,
			existingEpisodes: []int{4},
			expectUpToDate:   true,
			expectedLogs: []string{
				"[UP-TO-DATE] Hero: up to date at chapter 4",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runSyncerReportingCase(t, &tc)
		})
	}
}

func runSyncerReportingCase(t *testing.T, tc *reportTestCase) {
	t.Helper()

	seriesSlug := "tower"
	if tc.titleNo == 96 {
		seriesSlug = "hero"
	}

	tempDir := t.TempDir()
	seriesDir := filepath.Join(tempDir, seriesSlug)
	if err := os.MkdirAll(seriesDir, 0o700); err != nil {
		t.Fatalf("mkdir series dir: %v", err)
	}

	populateExistingFiles(t, seriesDir, tc.existingEpisodes, tc.legacyFiles)

	httpClient := &http.Client{
		Transport: roundTripFunc(mockSyncerTransport),
	}
	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
		webtoon.WithHTTPClient(httpClient),
		webtoon.WithSleepFunc(mockInstantSleep),
	)

	targetURL := fmt.Sprintf("https://www.webtoons.com/en/fantasy/%s/list?title_no=%d", seriesSlug, tc.titleNo)

	cfg := &config.Config{
		Directory:   tempDir,
		URL:         targetURL,
		Start:       tc.start,
		Format:      config.FormatPDF,
		Concurrency: 1,
		Delay:       0,
		Timeout:     5 * time.Second,
	}

	var logBuf bytes.Buffer
	syncInstance, err := syncer.New(cfg, client, &logBuf)
	if err != nil {
		t.Fatalf("create syncer: %v", err)
	}

	ctx := context.Background()
	if runErr := syncInstance.Run(ctx); runErr != nil {
		t.Fatalf("run syncer: %v", runErr)
	}

	assertReportingLogs(t, logBuf.String(), tc)
}

func populateExistingFiles(t *testing.T, seriesDir string, episodes []int, legacy []string) {
	t.Helper()

	for _, ep := range episodes {
		filePath := filepath.Join(seriesDir, fmt.Sprintf("%03d.pdf", ep))
		if err := os.WriteFile(filePath, []byte("dummy pdf content"), 0o600); err != nil {
			t.Fatalf("write existing file: %v", err)
		}
	}

	for _, legacyName := range legacy {
		filePath := filepath.Join(seriesDir, legacyName)
		if err := os.WriteFile(filePath, []byte("dummy pdf content"), 0o600); err != nil {
			t.Fatalf("write existing legacy file: %v", err)
		}
	}
}

func assertReportingLogs(t *testing.T, output string, tc *reportTestCase) {
	t.Helper()

	for _, expected := range tc.expectedLogs {
		if !strings.Contains(output, expected) {
			t.Errorf("expected output to contain %q, got:\n%s", expected, output)
		}
	}

	if strings.Contains(output, "[SKIPPED]") || strings.Contains(output, "[COMPLETED]") {
		t.Errorf("expected no per-chapter skipped or completed spam in logs, got:\n%s", output)
	}

	if tc.expectUpToDate {
		if strings.Contains(output, "[SERIES COMPLETED]") {
			t.Errorf("expected no completion logs when up to date, got:\n%s", output)
		}
		return
	}

	if strings.Contains(output, "[UP-TO-DATE]") {
		t.Errorf("expected no up to date logs when chapters were downloaded, got:\n%s", output)
	}
}

func TestSyncerImagesFormatDefaultSeriesJSON(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	httpClient := &http.Client{
		Transport: roundTripFunc(mockSyncerTransport),
	}
	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
		webtoon.WithHTTPClient(httpClient),
		webtoon.WithSleepFunc(mockInstantSleep),
	)

	cfg := &config.Config{
		Directory:   tempDir,
		URL:         "https://www.webtoons.com/en/fantasy/hero/list?title_no=96",
		Format:      config.FormatImages,
		Concurrency: 1,
		Delay:       0,
		Timeout:     5 * time.Second,
	}

	var logBuf bytes.Buffer
	syncInstance, err := syncer.New(cfg, client, &logBuf)
	if err != nil {
		t.Fatalf("create syncer: %v", err)
	}

	ctx := context.Background()
	if syncErr := syncInstance.Run(ctx); syncErr != nil {
		t.Fatalf("sync error: %v", syncErr)
	}

	seriesJSON := filepath.Join(tempDir, "hero", "series.json")
	if _, statErr := os.Stat(seriesJSON); statErr != nil {
		t.Errorf("expected series.json to exist by default for images format: %v", statErr)
	}
}

func TestSyncerInvalidFormatAndSilent(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Format: "unsupported_format"}
	if _, err := syncer.New(cfg, nil, io.Discard); err == nil {
		t.Fatalf("expected error creating syncer with unsupported format")
	}

	tempDir := t.TempDir()
	silentCfg := &config.Config{
		Directory:   tempDir,
		URL:         "https://www.webtoons.com/en/fantasy/palace/list?title_no=95",
		Format:      config.FormatPDF,
		Concurrency: 1,
		Silent:      true,
		Timeout:     5 * time.Second,
	}

	httpClient := &http.Client{
		Transport: roundTripFunc(mockSyncerTransport),
	}
	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
		webtoon.WithHTTPClient(httpClient),
		webtoon.WithSleepFunc(mockInstantSleep),
	)

	var logBuf bytes.Buffer
	s, err := syncer.New(silentCfg, client, &logBuf)
	if err != nil {
		t.Fatalf("failed to create silent syncer: %v", err)
	}

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("unexpected error running silent syncer: %v", err)
	}

	if logBuf.Len() != 0 {
		t.Errorf("expected no log output in silent mode, got %q", logBuf.String())
	}
}
