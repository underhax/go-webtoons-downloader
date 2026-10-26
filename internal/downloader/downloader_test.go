package downloader_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/underhax/go-webtoons-downloader/internal/downloader"
	"github.com/underhax/go-webtoons-downloader/internal/webtoon"
	"github.com/underhax/go-webtoons-downloader/internal/writer"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type recordingWriter struct {
	savedDest  string
	errToThrow error
	savedPages []writer.Page
}

func (r *recordingWriter) WriteChapter(_ context.Context, destPath string, pages []writer.Page, _ *writer.ChapterMetadata) error {
	if r.errToThrow != nil {
		return r.errToThrow
	}
	r.savedDest = destPath
	r.savedPages = pages
	return nil
}

func (r *recordingWriter) Extension() string {
	return ".mock"
}

func mockDownloaderTransport(req *http.Request) (*http.Response, error) {
	path := req.URL.Path

	switch path {
	case "/viewer/test", "/viewer/test-err", "/viewer/test-hook", "/viewer/test-delay", "/viewer/test-sem":
		body := `<div class="viewer_img _img_viewer_area"><img data-url="https://www.example.com/p1.jpg"><img data-url="https://www.example.com/p2.jpg"></div>`
		return createHTTPResponse(http.StatusOK, "text/html", body), nil

	case "/viewer/fail_img":
		body := `<div class="viewer_img _img_viewer_area"><img data-url="https://www.example.com/p_error.jpg"></div>`
		return createHTTPResponse(http.StatusOK, "text/html", body), nil

	case "/p1.jpg":
		return createHTTPResponse(http.StatusOK, "image/jpeg", "\xff\xd8\xffimage_bytes_1"), nil

	case "/p2.jpg":
		return createHTTPResponse(http.StatusOK, "image/jpeg", "\xff\xd8\xffimage_bytes_2"), nil

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

func mockInstantDownloaderSleep(ctx context.Context, _ time.Duration) bool {
	return ctx.Err() == nil
}

func TestDownloaderFlow(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{
		Transport: roundTripFunc(mockDownloaderTransport),
	}

	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.example.com", "https://m.example.com"),
		webtoon.WithHTTPClient(httpClient),
		webtoon.WithSleepFunc(mockInstantDownloaderSleep),
	)

	mockW := &recordingWriter{}
	dl := downloader.New(client, mockW, 2, 0)

	chapter := webtoon.Chapter{
		Number:    1,
		EpisodeNo: 1,
		Title:     "Episode 1",
		ViewerURL: "https://www.example.com/viewer/test",
	}

	ctx := context.Background()
	destPath := "/target/001.mock"

	var pagesDone atomic.Int32
	hooks := &downloader.ChapterHooks{
		OnPagesFetched: func(_ int) func() {
			return func() {}
		},
		OnPageDone: func(_, _ int) {
			pagesDone.Add(1)
		},
	}

	if err := dl.DownloadChapter(ctx, chapter, destPath, hooks, &writer.ChapterMetadata{}); err != nil {
		t.Fatalf("unexpected error downloading chapter: %v", err)
	}

	if pagesDone.Load() != 2 {
		t.Errorf("expected 2 page callbacks, got %d", pagesDone.Load())
	}

	if mockW.savedDest != destPath {
		t.Errorf("expected destination %s, got %s", destPath, mockW.savedDest)
	}

	if len(mockW.savedPages) != 2 {
		t.Fatalf("expected 2 pages saved, got %d", len(mockW.savedPages))
	}

	if string(mockW.savedPages[0].Data) != "\xff\xd8\xffimage_bytes_1" || mockW.savedPages[0].Index != 1 {
		t.Errorf("unexpected page 1 data: %+v", mockW.savedPages[0])
	}
	if string(mockW.savedPages[1].Data) != "\xff\xd8\xffimage_bytes_2" || mockW.savedPages[1].Index != 2 {
		t.Errorf("unexpected page 2 data: %+v", mockW.savedPages[1])
	}
}

func TestDownloaderErrors(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{
		Transport: roundTripFunc(mockDownloaderTransport),
	}

	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.example.com", "https://m.example.com"),
		webtoon.WithHTTPClient(httpClient),
		webtoon.WithSleepFunc(mockInstantDownloaderSleep),
	)

	mockErr := errors.New("write failure")
	mockW := &recordingWriter{errToThrow: mockErr}
	dl := downloader.New(client, mockW, 2, 0)

	chapter := webtoon.Chapter{
		Number:    1,
		ViewerURL: "https://www.example.com/viewer/test-err",
	}

	ctx := context.Background()
	err := dl.DownloadChapter(ctx, chapter, "/fail.mock", nil, &writer.ChapterMetadata{})
	if !errors.Is(err, mockErr) {
		t.Errorf("expected mockErr, got %v", err)
	}
}

func TestDownloaderScenarios(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{
		Transport: roundTripFunc(mockDownloaderTransport),
	}

	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.example.com", "https://m.example.com"),
		webtoon.WithHTTPClient(httpClient),
		webtoon.WithSleepFunc(mockInstantDownloaderSleep),
	)

	t.Run("default concurrency fallback", func(t *testing.T) {
		t.Parallel()
		dl := downloader.New(client, &recordingWriter{}, 0, 0)
		if dl == nil {
			t.Fatalf("expected non-nil downloader")
		}
	})

	t.Run("fetch chapter image urls failure", func(t *testing.T) {
		t.Parallel()
		dl := downloader.New(client, &recordingWriter{}, 2, 0)
		chapter := webtoon.Chapter{
			Number:    1,
			ViewerURL: "https://www.example.com/notfound",
		}
		if err := dl.DownloadChapter(context.Background(), chapter, "/path", nil, nil); err == nil {
			t.Fatalf("expected error for not found viewer url")
		}
	})

	t.Run("context canceled in on pages fetched hook", func(t *testing.T) {
		t.Parallel()
		dl := downloader.New(client, &recordingWriter{}, 2, 0)
		chapter := webtoon.Chapter{
			Number:    1,
			ViewerURL: "https://www.example.com/viewer/test-hook",
		}
		ctx, cancel := context.WithCancel(context.Background())
		hookCleanedUp := false
		hooks := &downloader.ChapterHooks{
			OnPagesFetched: func(_ int) func() {
				cancel()
				return func() {
					hookCleanedUp = true
				}
			},
		}
		err := dl.DownloadChapter(ctx, chapter, "/path", hooks, nil)
		if err == nil {
			t.Fatalf("expected context canceled error")
		}
		if !hookCleanedUp {
			t.Errorf("expected hook clean up func to run")
		}
	})

	t.Run("page download failure", func(t *testing.T) {
		t.Parallel()
		dl := downloader.New(client, &recordingWriter{}, 2, 0)
		chapter := webtoon.Chapter{
			Number:    1,
			ViewerURL: "https://www.example.com/viewer/fail_img",
		}
		err := dl.DownloadChapter(context.Background(), chapter, "/path", nil, nil)
		if err == nil {
			t.Fatalf("expected error downloading page")
		}
	})

	t.Run("delay interrupted by context cancellation", func(t *testing.T) {
		t.Parallel()
		dl := downloader.New(client, &recordingWriter{}, 1, 500*time.Millisecond)
		chapter := webtoon.Chapter{
			Number:    1,
			ViewerURL: "https://www.example.com/viewer/test-delay",
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := dl.DownloadChapter(ctx, chapter, "/path", nil, nil)
		if err == nil {
			t.Fatalf("expected timeout/cancellation error during delay")
		}
	})

	t.Run("semaphore blocked worker interrupted by context cancellation", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		workerStarted := make(chan struct{})

		blockingTransport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/p1.jpg" {
				close(workerStarted)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return mockDownloaderTransport(req)
		})

		blockingClient := webtoon.NewClient(
			5*time.Second,
			webtoon.WithBaseURLs("https://www.example.com", "https://m.example.com"),
			webtoon.WithHTTPClient(&http.Client{Transport: blockingTransport}),
		)

		dl := downloader.New(blockingClient, &recordingWriter{}, 1, 0)
		chapter := webtoon.Chapter{
			Number:    1,
			ViewerURL: "https://www.example.com/viewer/test-sem",
		}

		go func() {
			<-workerStarted
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()

		err := dl.DownloadChapter(ctx, chapter, "/path", nil, nil)
		if err == nil {
			t.Fatalf("expected error waiting on semaphore")
		}
	})
}
