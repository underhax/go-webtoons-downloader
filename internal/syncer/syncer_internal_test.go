package syncer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/underhax/go-webtoons-downloader/internal/config"
	"github.com/underhax/go-webtoons-downloader/internal/downloader"
	"github.com/underhax/go-webtoons-downloader/internal/ui"
	"github.com/underhax/go-webtoons-downloader/internal/validator"
	"github.com/underhax/go-webtoons-downloader/internal/webtoon"
	"github.com/underhax/go-webtoons-downloader/internal/writer/pdf"
)

func TestMain(m *testing.M) {
	origLstat := validator.LstatFunc
	validator.LstatFunc = func(string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	code := m.Run()
	validator.LstatFunc = origLstat
	os.Exit(code)
}

type failWriter struct{}

func (f *failWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("simulated write failure")
}

type errReader struct{}

func (e *errReader) Read(_ []byte) (int, error) {
	return 0, errors.New("simulated read failure")
}

func mockFailingClose(_ *os.File) error {
	return errors.New("simulated file close failure")
}

func mockFailingJSONMarshal(_ any, _, _ string) ([]byte, error) {
	return nil, errors.New("simulated json marshal failure")
}

func mockFailingOSWriteFile(_ string, _ []byte, _ os.FileMode) error {
	return errors.New("simulated write file failure")
}

func mockFailingOSRename(_, _ string) error {
	return errors.New("simulated os rename failure")
}

func mockFailingDetectLastChapter(_, _ string) (int, error) {
	return 0, errors.New("simulated detect last chapter failure")
}

func mockFailingEpisodesTransport(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Path, "episodes") {
		return makeSyncerResponse(http.StatusInternalServerError, "", strings.NewReader("server error")), nil
	}
	return mockInternalSyncerTransport(req)
}

func mockFailingViewerTransport(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Path, "viewer") {
		return makeSyncerResponse(http.StatusInternalServerError, "", strings.NewReader("server error")), nil
	}
	return mockInternalSyncerTransport(req)
}

type syncerInternalRoundTripFunc func(req *http.Request) (*http.Response, error)

func (f syncerInternalRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func makeSyncerResponse(status int, contentType string, body io.Reader) *http.Response {
	resp := &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(body),
	}
	if contentType != "" {
		resp.Header.Set("Content-Type", contentType)
	}
	return resp
}

func mockInternalSyncerTransport(req *http.Request) (*http.Response, error) {
	path := req.URL.Path
	switch path {
	case "/en/fantasy/tower/list":
		body := `<html><head><link rel="canonical" href="https://www.webtoons.com/en/fantasy/tower/list?title_no=95"><meta property="og:title" content="Tower of God"></head><body></body></html>`
		return makeSyncerResponse(http.StatusOK, "text/html", strings.NewReader(body)), nil
	case "/api/v1/webtoon/95/episodes":
		body := `{"result":{"episodeList":[{"episodeNo":1,"episodeTitle":"Episode 1","viewerLink":"/en/fantasy/tower/viewer?episode_no=1"}]}}`
		return makeSyncerResponse(http.StatusOK, "application/json", strings.NewReader(body)), nil
	case "/en/fantasy/tower/viewer":
		body := `<div class="viewer_img _img_viewer_area"><img data-url="https://www.webtoons.com/img/p1.jpg"></div>`
		return makeSyncerResponse(http.StatusOK, "text/html", strings.NewReader(body)), nil
	case "/img/p1.jpg":
		jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xC0, 0x00, 0x11, 0x08, 0x00, 0x64, 0x00, 0x64, 0x03, 0x01, 0x11, 0x00, 0x02, 0x11, 0x01, 0x03, 0x11, 0x01}
		return makeSyncerResponse(http.StatusOK, "image/jpeg", bytes.NewReader(jpegHeader)), nil
	default:
		return makeSyncerResponse(http.StatusNotFound, "", strings.NewReader("not found")), nil
	}
}

func TestDefaultSyncerInternalOperations(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp(t.TempDir(), "dummy-syncer-*")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}

	if cerr := defaultFileClose(f); cerr != nil {
		t.Errorf("expected successful close, got: %v", cerr)
	}

	if cerr := defaultFileClose(f); cerr == nil {
		t.Errorf("expected error closing already closed file, got nil")
	}

	data, err := defaultJSONMarshalIndent(map[string]string{"k": "v"}, "", "  ")
	if err != nil || len(data) == 0 {
		t.Errorf("expected json marshal success, got data: %v, err: %v", data, err)
	}

	target := filepath.Join(t.TempDir(), "write_test.txt")
	if werr := defaultOSWriteFile(target, []byte("hello"), 0o600); werr != nil {
		t.Errorf("expected os write file success, got: %v", werr)
	}

	invalidTarget := filepath.Join(t.TempDir(), "non_existent_dir", "file.txt")
	if ierr := defaultOSWriteFile(invalidTarget, []byte("hello"), 0o600); ierr == nil {
		t.Errorf("expected os write file failure for non-existent directory, got nil")
	}

	chNum, err := defaultDetectLastChapter(t.TempDir(), ".pdf")
	if err != nil || chNum != 0 {
		t.Errorf("expected 0, nil from defaultDetectLastChapter, got %d, %v", chNum, err)
	}
}

func TestParseLinksScannerError(t *testing.T) {
	t.Parallel()

	_, err := ParseLinks(&errReader{})
	if err == nil || !strings.Contains(err.Error(), "read links content") {
		t.Errorf("expected read links content error, got: %v", err)
	}
}

func TestParseLinksFileCloseFailure(t *testing.T) {
	origClose := fileClose
	defer func() {
		fileClose = origClose
	}()

	tempDir := t.TempDir()
	linksFile := filepath.Join(tempDir, "links.txt")
	if err := os.WriteFile(linksFile, []byte("https://www.webtoons.com/en/fantasy/tower/list?title_no=95\n"), 0o600); err != nil {
		t.Fatalf("write links file: %v", err)
	}

	fileClose = mockFailingClose
	entries, err := ParseLinksFile(linksFile)
	fileClose = origClose

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

func TestDetectLastChapterReadDirFailure(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	regularFile := filepath.Join(tempDir, "not_a_dir")
	if err := os.WriteFile(regularFile, []byte("content"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := DetectLastChapter(regularFile, ".pdf")
	if err == nil || !strings.Contains(err.Error(), "read chapter directory") {
		t.Errorf("expected read dir failure on regular file, got: %v", err)
	}
}

func TestTruncateUTF8Fallback(t *testing.T) {
	t.Parallel()

	raw := "\x80\x80\x80\x80"
	truncated := truncateUTF8(raw, 2)
	if truncated != raw[:2] {
		t.Errorf("expected %q, got %q", raw[:2], truncated)
	}
}

func TestResolveWriterInvalidFormat(t *testing.T) {
	t.Parallel()

	_, err := resolveWriter("unknown_format")
	if err == nil || !errors.Is(err, config.ErrInvalidFormat) {
		t.Errorf("expected ErrInvalidFormat, got: %v", err)
	}
}

func TestFindEntryByIndexNotFound(t *testing.T) {
	t.Parallel()

	entries := []LinkEntry{
		{Index: 1, URL: "https://www.webtoons.com/en/fantasy/one/list?title_no=1"},
		{Index: 2, URL: "https://www.webtoons.com/en/fantasy/two/list?title_no=2"},
	}

	_, found := findEntryByIndex(entries, 99)
	if found {
		t.Errorf("expected not found for index 99")
	}
}

func TestWriteSeriesJSONFailures(t *testing.T) {
	origMarshal := jsonMarshalIndent
	origWriteFile := osWriteFile
	origRename := osRename
	defer func() {
		jsonMarshalIndent = origMarshal
		osWriteFile = origWriteFile
		osRename = origRename
	}()

	meta := &webtoon.SeriesInfo{
		TitleName: "SeriesA",
		Author:    "AuthorOne, , AuthorTwo",
		Genre:     "Action",
		Summary:   "Summary",
	}

	t.Run("marshal error", func(t *testing.T) {
		tempDir := t.TempDir()
		jsonMarshalIndent = mockFailingJSONMarshal
		err := writeSeriesJSON(tempDir, meta, "https://www.webtoons.com/en/fantasy/tower/list?title_no=95")
		jsonMarshalIndent = origMarshal

		if err == nil || !strings.Contains(err.Error(), "marshal series json") {
			t.Errorf("expected marshal error, got: %v", err)
		}
	})

	t.Run("write file error", func(t *testing.T) {
		tempDir := t.TempDir()
		osWriteFile = mockFailingOSWriteFile
		err := writeSeriesJSON(tempDir, meta, "https://www.webtoons.com/en/fantasy/tower/list?title_no=95")
		osWriteFile = origWriteFile

		if err == nil || !strings.Contains(err.Error(), "write tmp series json") {
			t.Errorf("expected write file error, got: %v", err)
		}
	})

	t.Run("rename error", func(t *testing.T) {
		tempDir := t.TempDir()
		osRename = mockFailingOSRename
		err := writeSeriesJSON(tempDir, meta, "https://www.webtoons.com/en/fantasy/tower/list?title_no=95")
		osRename = origRename

		if err == nil || !strings.Contains(err.Error(), "atomic rename series json") {
			t.Errorf("expected rename error, got: %v", err)
		}
	})

	t.Run("validate write target error", func(t *testing.T) {
		err := writeSeriesJSON("../escape_series_json", meta, "https://www.webtoons.com/en/fantasy/tower/list?title_no=95")
		if err == nil || !strings.Contains(err.Error(), "validate series json path") {
			t.Errorf("expected validate series json path error, got: %v", err)
		}
	})
}

func TestFormatChapterListBranches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expected string
		chapters []int
	}{
		{chapters: []int{}, expected: ""},
		{chapters: []int{10}, expected: "10"},
		{chapters: []int{1, 2}, expected: "1, 2"},
		{chapters: []int{1, 2, 3, 4, 5}, expected: "1-5"},
		{chapters: []int{1, 2, 4, 6, 7, 8}, expected: "1, 2, 4, 6-8"},
	}

	for _, tc := range tests {
		actual := formatChapterList(tc.chapters)
		if actual != tc.expected {
			t.Errorf("chapters %v: expected %q, got %q", tc.chapters, tc.expected, actual)
		}
	}
}

func TestLogAlreadyUpToDateBranches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expectedOutput string
		manualStart    int
		lastExisting   int
		total          int
		silent         bool
	}{
		{manualStart: 10, lastExisting: 0, total: 5, silent: false, expectedOutput: "start chapter 10 exceeds available chapters (5)"},
		{manualStart: 0, lastExisting: 10, total: 10, silent: false, expectedOutput: "up to date at chapter 10"},
		{manualStart: 0, lastExisting: 0, total: 0, silent: false, expectedOutput: "no chapters available"},
		{manualStart: 0, lastExisting: 0, total: 0, silent: true, expectedOutput: ""},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		s := &Syncer{
			cfg: &config.Config{Silent: tc.silent},
			out: &buf,
		}

		err := s.logAlreadyUpToDate("SeriesB", tc.manualStart, tc.lastExisting, tc.total)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		actual := buf.String()
		if tc.expectedOutput != "" && !strings.Contains(actual, tc.expectedOutput) {
			t.Errorf("expected %q in output, got %q", tc.expectedOutput, actual)
		}
		if tc.silent && actual != "" {
			t.Errorf("expected empty output when silent, got %q", actual)
		}
	}
}

func TestLogNoDownloadsBranches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expectedOutput  string
		skippedChapters []int
		totalChapters   int
	}{
		{skippedChapters: []int{}, totalChapters: 15, expectedOutput: "up to date at chapter 15"},
		{skippedChapters: []int{7}, totalChapters: 15, expectedOutput: "chapter 7 already present"},
		{skippedChapters: []int{7, 8}, totalChapters: 15, expectedOutput: "chapters 7, 8 already present"},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		s := &Syncer{
			cfg: &config.Config{Silent: false},
			out: &buf,
		}

		res := deltaResult{
			skippedChapters: tc.skippedChapters,
			totalChapters:   tc.totalChapters,
		}

		err := s.logNoDownloads("SeriesC", res)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		actual := buf.String()
		if !strings.Contains(actual, tc.expectedOutput) {
			t.Errorf("expected %q in output, got %q", tc.expectedOutput, actual)
		}
	}
}

func TestLogDownloadsWithoutSkipsBranches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expectedOutput  string
		startChapter    int
		totalChapters   int
		downloadedCount int
	}{
		{startChapter: 1, totalChapters: 5, downloadedCount: 5, expectedOutput: "all 5 chapters downloaded"},
		{startChapter: 2, totalChapters: 5, downloadedCount: 1, expectedOutput: "1 chapter downloaded"},
		{startChapter: 2, totalChapters: 5, downloadedCount: 3, expectedOutput: "3 chapters downloaded"},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		s := &Syncer{
			cfg: &config.Config{Silent: false},
			out: &buf,
		}

		res := deltaResult{
			startChapter:    tc.startChapter,
			totalChapters:   tc.totalChapters,
			downloadedCount: tc.downloadedCount,
		}

		err := s.logDownloadsWithoutSkips("SeriesD", res)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		actual := buf.String()
		if !strings.Contains(actual, tc.expectedOutput) {
			t.Errorf("expected %q in output, got %q", tc.expectedOutput, actual)
		}
	}
}

func TestLogDownloadsWithSkipsBranches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expectedOutput  string
		skippedChapters []int
		startChapter    int
		totalChapters   int
		downloadedCount int
	}{
		{startChapter: 1, totalChapters: 5, downloadedCount: 1, skippedChapters: []int{2, 3, 4, 5}, expectedOutput: "1 of 5 chapters downloaded (skipped chapters: 2-5)"},
		{startChapter: 1, totalChapters: 5, downloadedCount: 2, skippedChapters: []int{3, 4, 5}, expectedOutput: "2 of 5 chapters downloaded (skipped chapters: 3-5)"},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		s := &Syncer{
			cfg: &config.Config{Silent: false},
			out: &buf,
		}

		res := deltaResult{
			startChapter:    tc.startChapter,
			totalChapters:   tc.totalChapters,
			downloadedCount: tc.downloadedCount,
			skippedChapters: tc.skippedChapters,
		}

		err := s.logDownloadsWithSkips("SeriesE", res)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		actual := buf.String()
		if !strings.Contains(actual, tc.expectedOutput) {
			t.Errorf("expected %q in output, got %q", tc.expectedOutput, actual)
		}
	}
}

func TestShouldWriteSeriesJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		format   string
		series   bool
		expected bool
	}{
		{format: config.FormatPDF, series: true, expected: true},
		{format: config.FormatImages, series: false, expected: true},
		{format: config.FormatPDF, series: false, expected: false},
		{format: config.FormatCBZ, series: false, expected: false},
	}

	for _, tc := range tests {
		s := &Syncer{
			cfg: &config.Config{Format: tc.format, SeriesJSON: tc.series},
		}
		if actual := s.shouldWriteSeriesJSON(); actual != tc.expected {
			t.Errorf("format %s, series %v: expected %v, got %v", tc.format, tc.series, tc.expected, actual)
		}
	}
}

func TestResolveWriterAllFormats(t *testing.T) {
	t.Parallel()

	formats := []string{
		config.FormatPDF,
		config.FormatCBZ,
		config.FormatZIP,
		config.FormatImages,
	}

	for _, f := range formats {
		w, err := resolveWriter(f)
		if err != nil || w == nil {
			t.Errorf("format %s: expected valid writer, got err: %v", f, err)
		}
	}
}

func TestFindEntryByIndexFound(t *testing.T) {
	t.Parallel()

	entries := []LinkEntry{
		{Index: 1, URL: "https://www.webtoons.com/en/fantasy/one/list?title_no=1"},
		{Index: 2, URL: "https://www.webtoons.com/en/fantasy/two/list?title_no=2"},
	}

	entry, found := findEntryByIndex(entries, 2)
	if !found || entry.Index != 2 {
		t.Errorf("expected entry with index 2, got %v, found: %v", entry, found)
	}
}

func TestLogWriterErrors(t *testing.T) {
	t.Parallel()

	s := &Syncer{
		cfg: &config.Config{Silent: false},
		out: &failWriter{},
	}

	testCases := []func(title string) error{
		func(seriesTitle string) error { return s.logAlreadyUpToDate(seriesTitle, 10, 0, 5) },
		func(seriesTitle string) error { return s.logAlreadyUpToDate(seriesTitle, 0, 5, 5) },
		func(seriesTitle string) error { return s.logAlreadyUpToDate(seriesTitle, 0, 0, 0) },
		func(seriesTitle string) error {
			return s.logNoDownloads(seriesTitle, deltaResult{skippedChapters: []int{1}})
		},
		func(seriesTitle string) error {
			return s.logNoDownloads(seriesTitle, deltaResult{skippedChapters: []int{1, 2}})
		},
		func(seriesTitle string) error {
			return s.logNoDownloads(seriesTitle, deltaResult{skippedChapters: []int{}})
		},
		func(seriesTitle string) error {
			return s.logDownloadsWithoutSkips(seriesTitle, deltaResult{startChapter: 1, totalChapters: 2, downloadedCount: 2})
		},
		func(seriesTitle string) error {
			return s.logDownloadsWithoutSkips(seriesTitle, deltaResult{startChapter: 2, totalChapters: 5, downloadedCount: 1})
		},
		func(seriesTitle string) error {
			return s.logDownloadsWithoutSkips(seriesTitle, deltaResult{startChapter: 2, totalChapters: 5, downloadedCount: 3})
		},
		func(seriesTitle string) error {
			res := deltaResult{startChapter: 1, totalChapters: 5, downloadedCount: 1, skippedChapters: []int{2}}
			return s.logDownloadsWithSkips(seriesTitle, res)
		},
		func(seriesTitle string) error {
			res := deltaResult{startChapter: 1, totalChapters: 5, downloadedCount: 2, skippedChapters: []int{2}}
			return s.logDownloadsWithSkips(seriesTitle, res)
		},
	}

	for i, fn := range testCases {
		if err := fn("LogSeries"); err == nil {
			t.Errorf("case %d: expected write error in log, got nil", i)
		}
	}
}

func TestSyncAllEntriesErrors(t *testing.T) {
	t.Parallel()

	entries := []LinkEntry{{Index: 1, URL: "https://www.webtoons.com/en/fantasy/tower/list?title_no=95"}}

	t.Run("context canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		s := &Syncer{
			cfg: &config.Config{Silent: true},
			out: &bytes.Buffer{},
		}
		err := s.syncAllEntries(ctx, entries)
		if err == nil || !strings.Contains(err.Error(), "sync aborted") {
			t.Errorf("expected sync aborted error, got: %v", err)
		}
	})

	t.Run("error log write failure", func(t *testing.T) {
		httpClient := &http.Client{Transport: syncerInternalRoundTripFunc(mockInternalSyncerTransport)}
		client := webtoon.NewClient(
			5*time.Second,
			webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
			webtoon.WithHTTPClient(httpClient),
		)

		s := &Syncer{
			cfg:    &config.Config{Directory: t.TempDir(), Format: config.FormatPDF},
			client: client,
			out:    &failWriter{},
		}

		err := s.syncAllEntries(context.Background(), []LinkEntry{{Index: 1, URL: "https://www.webtoons.com/en/broken/slug/list?title_no=9999"}})
		if err == nil || !strings.Contains(err.Error(), "write error log") {
			t.Errorf("expected write error log failure, got: %v", err)
		}
	})
}

func TestRunBaseDirFailureAndTargetIndex(t *testing.T) {
	t.Parallel()

	invalidDirSyncer := &Syncer{
		cfg: &config.Config{Directory: "../escape_base_dir"},
	}
	if invalidErr := invalidDirSyncer.Run(context.Background()); invalidErr == nil || !strings.Contains(invalidErr.Error(), "validate base directory") {
		t.Errorf("expected validate base directory error, got: %v", invalidErr)
	}

	tempDir := t.TempDir()
	regularFile := filepath.Join(tempDir, "base_is_file")
	if err := os.WriteFile(regularFile, []byte("file"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	s := &Syncer{
		cfg: &config.Config{Directory: regularFile},
	}
	err := s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "create base directory") {
		t.Errorf("expected create base directory error, got: %v", err)
	}

	linksPath := filepath.Join(tempDir, "links.txt")
	if err := os.WriteFile(linksPath, []byte("https://www.webtoons.com/en/fantasy/tower/list?title_no=95\n"), 0o600); err != nil {
		t.Fatalf("write links file: %v", err)
	}

	httpClient := &http.Client{Transport: syncerInternalRoundTripFunc(mockInternalSyncerTransport)}
	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
		webtoon.WithHTTPClient(httpClient),
	)
	w := pdf.New()
	dl := downloader.New(client, w, 1, 0)

	syncerInst := &Syncer{
		cfg: &config.Config{
			Directory: tempDir,
			LinksFile: linksPath,
			Index:     1,
			Format:    config.FormatPDF,
			Silent:    true,
		},
		client:     client,
		writer:     w,
		downloader: dl,
		out:        &bytes.Buffer{},
	}

	if runErr := syncerInst.Run(context.Background()); runErr != nil {
		t.Errorf("expected successful run with target index 1, got: %v", runErr)
	}
}

func TestSyncSingleURLErrorsAndHooks(t *testing.T) {
	tempDir := t.TempDir()
	httpClient := &http.Client{Transport: syncerInternalRoundTripFunc(mockInternalSyncerTransport)}
	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
		webtoon.WithHTTPClient(httpClient),
	)
	w := pdf.New()
	dl := downloader.New(client, w, 1, 0)

	t.Run("folder path exists as file", func(t *testing.T) {
		meta, err := client.FetchSeriesMetadata(context.Background(), "https://www.webtoons.com/en/fantasy/tower/list?title_no=95")
		if err != nil {
			t.Fatalf("fetch metadata: %v", err)
		}
		folderName := BuildFolderName(1, meta.TitleSlug)
		blockerFile := filepath.Join(tempDir, folderName)
		if werr := os.WriteFile(blockerFile, []byte("blocker"), 0o600); werr != nil {
			t.Fatalf("write blocker: %v", werr)
		}

		s := &Syncer{
			cfg: &config.Config{
				Directory: tempDir,
				Format:    config.FormatPDF,
				Silent:    true,
			},
			client: client,
			writer: w,
		}

		err = s.syncSingleURL(context.Background(), "https://www.webtoons.com/en/fantasy/tower/list?title_no=95", 1, 0)
		if err == nil || !strings.Contains(err.Error(), "create series folder") {
			t.Errorf("expected create series folder error, got: %v", err)
		}
	})

	t.Run("detect last chapter failure", func(t *testing.T) {
		origDetect := detectLastChapterFunc
		defer func() {
			detectLastChapterFunc = origDetect
		}()

		detectLastChapterFunc = mockFailingDetectLastChapter
		s := &Syncer{
			cfg: &config.Config{
				Directory: t.TempDir(),
				Format:    config.FormatPDF,
				Silent:    true,
			},
			client: client,
			writer: w,
		}

		err := s.syncSingleURL(context.Background(), "https://www.webtoons.com/en/fantasy/tower/list?title_no=95", 2, 0)
		detectLastChapterFunc = origDetect

		if err == nil || !strings.Contains(err.Error(), "detect last chapter") {
			t.Errorf("expected detect last chapter error, got: %v", err)
		}
	})

	t.Run("downloadChapterDelta canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		s := &Syncer{
			cfg:    &config.Config{Directory: tempDir, Format: config.FormatPDF, Silent: true},
			client: client,
			writer: w,
		}

		chapters := []webtoon.Chapter{{Number: 1}}
		meta := &webtoon.SeriesInfo{TitleName: "DeltaSeries"}
		err := s.downloadChapterDelta(ctx, chapters, 1, tempDir, meta)
		if err == nil || !strings.Contains(err.Error(), "delta download canceled") {
			t.Errorf("expected delta download canceled error, got: %v", err)
		}
	})

	t.Run("downloadChapterIfMissing exists and hooks executed", func(t *testing.T) {
		chapterDir := t.TempDir()
		existingFile := filepath.Join(chapterDir, "001.pdf")
		if err := os.WriteFile(existingFile, []byte("chapter1"), 0o600); err != nil {
			t.Fatalf("write existing chapter: %v", err)
		}

		var buf bytes.Buffer
		s := &Syncer{
			cfg:        &config.Config{Directory: chapterDir, Format: config.FormatPDF, Silent: false},
			client:     client,
			writer:     w,
			downloader: dl,
			out:        &buf,
		}

		ch := webtoon.Chapter{Number: 1, Title: "Ch1"}
		meta := &webtoon.SeriesInfo{TitleName: "EchoSeries"}
		downloaded, err := s.downloadChapterIfMissing(context.Background(), ch, chapterDir, "%03d.pdf", 1, meta, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if downloaded {
			t.Errorf("expected downloaded=false for existing chapter file")
		}
	})
}

func TestBuildChapterHooksAndSpinnerTransitions(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	s := &Syncer{
		cfg: &config.Config{Silent: false},
		out: &buf,
	}

	var sp *ui.Spinner
	hooks1 := s.buildChapterHooks(context.Background(), "Chapter 1", &sp)
	if sp == nil || hooks1 == nil {
		t.Fatalf("expected non-nil spinner and hooks on first build")
	}
	closer1 := hooks1.OnPagesFetched(5)
	if closer1 != nil {
		t.Errorf("expected nil closer from OnPagesFetched")
	}
	hooks1.OnPageDone(1, 5)

	hooks2 := s.buildChapterHooks(context.Background(), "Chapter 2", &sp)
	if hooks2 == nil {
		t.Fatalf("expected non-nil hooks on second build")
	}
	closer2 := hooks2.OnPagesFetched(10)
	if closer2 != nil {
		t.Errorf("expected nil closer from OnPagesFetched")
	}
	hooks2.OnPageDone(1, 10)

	sp.Stop()

	nilPtrHooks := s.buildChapterHooks(context.Background(), "Chapter Nil", nil)
	if nilPtrHooks == nil {
		t.Fatalf("expected non-nil hooks with nil spPtr")
	}
	closerNil := nilPtrHooks.OnPagesFetched(3)
	if closerNil != nil {
		t.Errorf("expected nil closer from OnPagesFetched with nil spPtr")
	}
	nilPtrHooks.OnPageDone(1, 3)
}

func TestRunLoadLinksFailure(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	s := &Syncer{
		cfg: &config.Config{
			Directory: tempDir,
			LinksFile: filepath.Join(tempDir, "missing_links.txt"),
		},
	}
	err := s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "load links") {
		t.Errorf("expected load links error, got: %v", err)
	}
}

func TestSyncSingleURLDownloadAndNamingBranches(t *testing.T) {
	tempDir := t.TempDir()
	httpClient := &http.Client{Transport: syncerInternalRoundTripFunc(mockInternalSyncerTransport)}
	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
		webtoon.WithHTTPClient(httpClient),
	)
	w := pdf.New()

	t.Run("naming title format", func(t *testing.T) {
		s := &Syncer{
			cfg: &config.Config{
				Directory: tempDir,
				Naming:    config.NamingTitle,
				Format:    config.FormatPDF,
				Silent:    true,
				Start:     999,
			},
			client: client,
			writer: w,
		}
		if err := s.syncSingleURL(context.Background(), "https://www.webtoons.com/en/fantasy/tower/list?title_no=95", 1, 999); err != nil {
			t.Errorf("unexpected error in naming title: %v", err)
		}
	})

	t.Run("write series json failure", func(t *testing.T) {
		origWrite := osWriteFile
		defer func() { osWriteFile = origWrite }()
		osWriteFile = mockFailingOSWriteFile

		s := &Syncer{
			cfg: &config.Config{
				Directory:  tempDir,
				SeriesJSON: true,
				Format:     config.FormatPDF,
				Silent:     true,
			},
			client: client,
			writer: w,
		}
		err := s.syncSingleURL(context.Background(), "https://www.webtoons.com/en/fantasy/tower/list?title_no=95", 1, 0)
		if err == nil || !strings.Contains(err.Error(), "write series metadata json") {
			t.Errorf("expected write series json failure, got: %v", err)
		}
	})

	t.Run("fetch chapters failure", func(t *testing.T) {
		badClient := webtoon.NewClient(
			5*time.Second,
			webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
			webtoon.WithHTTPClient(&http.Client{Transport: syncerInternalRoundTripFunc(mockFailingEpisodesTransport)}),
		)
		s := &Syncer{
			cfg: &config.Config{
				Directory: tempDir,
				Format:    config.FormatPDF,
				Silent:    true,
			},
			client: badClient,
			writer: w,
		}
		err := s.syncSingleURL(context.Background(), "https://www.webtoons.com/en/fantasy/tower/list?title_no=95", 1, 0)
		if err == nil || !strings.Contains(err.Error(), "fetch chapters for") {
			t.Errorf("expected fetch chapters failure, got: %v", err)
		}
	})

	t.Run("downloadChapterIfMissing failure", func(t *testing.T) {
		badClient := webtoon.NewClient(
			5*time.Second,
			webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
			webtoon.WithHTTPClient(&http.Client{Transport: syncerInternalRoundTripFunc(mockFailingViewerTransport)}),
		)
		badDl := downloader.New(badClient, w, 1, 0)
		s := &Syncer{
			cfg: &config.Config{
				Directory: t.TempDir(),
				Format:    config.FormatPDF,
				Silent:    true,
			},
			client:     badClient,
			writer:     w,
			downloader: badDl,
		}
		err := s.syncSingleURL(context.Background(), "https://www.webtoons.com/en/fantasy/tower/list?title_no=95", 1, 0)
		if err == nil || !strings.Contains(err.Error(), "download chapter 1") {
			t.Errorf("expected download chapter failure, got: %v", err)
		}
	})

	t.Run("syncSingleURL validate folder path failure", func(t *testing.T) {
		s := &Syncer{
			cfg: &config.Config{
				Directory: "../escape_folder",
				Format:    config.FormatPDF,
				Silent:    true,
			},
			client: client,
			writer: w,
		}
		err := s.syncSingleURL(context.Background(), "https://www.webtoons.com/en/fantasy/tower/list?title_no=95", 1, 0)
		if err == nil || !strings.Contains(err.Error(), "validate series folder path") {
			t.Errorf("expected validate series folder path error, got: %v", err)
		}
	})

	t.Run("downloadChapterIfMissing validate chapter path failure", func(t *testing.T) {
		s := &Syncer{
			cfg:    &config.Config{Directory: tempDir, Format: config.FormatPDF, Silent: true},
			client: client,
			writer: w,
		}
		ch := webtoon.Chapter{Number: 1, Title: "Ch1"}
		meta := &webtoon.SeriesInfo{TitleName: "SeriesX"}
		_, err := s.downloadChapterIfMissing(context.Background(), ch, "../escape_chapter", "%03d.pdf", 1, meta, nil)
		if err == nil || !strings.Contains(err.Error(), "validate chapter path") {
			t.Errorf("expected validate chapter path error, got: %v", err)
		}
	})
}

type fakeDirEntry struct {
	name  string
	mode  os.FileMode
	isDir bool
}

func (f fakeDirEntry) Name() string               { return f.name }
func (f fakeDirEntry) IsDir() bool                { return f.isDir }
func (f fakeDirEntry) Type() os.FileMode          { return f.mode }
func (f fakeDirEntry) Info() (os.FileInfo, error) { return nil, os.ErrNotExist }

func TestDetectLastChapterSymlinksAndSubdirs(t *testing.T) {
	origReadDir := osReadDir
	t.Cleanup(func() {
		osReadDir = origReadDir
	})

	osReadDir = func(string) ([]os.DirEntry, error) {
		return []os.DirEntry{
			fakeDirEntry{name: "999.pdf", mode: os.ModeSymlink, isDir: false},
			fakeDirEntry{name: "005.pdf", mode: os.ModeDir, isDir: true},
			fakeDirEntry{name: "003.pdf", mode: 0, isDir: false},
		}, nil
	}

	lastPDF, err := DetectLastChapter("mock_dir", ".pdf")
	if err != nil || lastPDF != 3 {
		t.Fatalf("expected last chapter 3 (ignoring symlink 999 and dir 005), got %d, err %v", lastPDF, err)
	}

	lastImages, err := DetectLastChapter("mock_dir", "")
	if err != nil || lastImages != 5 {
		t.Fatalf("expected last images chapter 5 (only dir), got %d, err %v", lastImages, err)
	}
}
