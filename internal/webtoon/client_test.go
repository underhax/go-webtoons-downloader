package webtoon_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/underhax/go-webtoons-downloader/internal/webtoon"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestParseSeriesURLTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expectErr  error
		name       string
		rawURL     string
		expectLang string
		expectSlug string
		expectNo   int
		isCanvas   bool
	}{
		{
			expectErr:  nil,
			name:       "valid original series url with title_no",
			rawURL:     "https://www.webtoons.com/en/fantasy/tower-of-god/list?title_no=95",
			expectLang: "en",
			expectSlug: "tower-of-god",
			expectNo:   95,
			isCanvas:   false,
		},
		{
			expectErr:  nil,
			name:       "valid canvas series url",
			rawURL:     "https://m.webtoons.com/fr/canvas/my-comic/list?title_no=123",
			expectLang: "fr",
			expectSlug: "my-comic",
			expectNo:   123,
			isCanvas:   true,
		},
		{
			expectErr:  nil,
			name:       "valid series url without scheme",
			rawURL:     "webtoons.com/zh-hant/drama/my-title/list?title_no=456",
			expectLang: "zh-hant",
			expectSlug: "my-title",
			expectNo:   456,
			isCanvas:   false,
		},
		{
			expectErr: webtoon.ErrInvalidURL,
			name:      "empty url rejected",
			rawURL:    "",
		},
		{
			expectErr: webtoon.ErrInvalidURL,
			name:      "unsupported scheme rejected",
			rawURL:    "ftp://www.webtoons.com/en/fantasy/tower/list?title_no=95",
		},
		{
			expectErr: webtoon.ErrInvalidURL,
			name:      "insecure http scheme rejected",
			rawURL:    "http://www.webtoons.com/en/fantasy/tower/list?title_no=95",
		},
		{
			expectErr: webtoon.ErrInvalidURL,
			name:      "userinfo rejected",
			rawURL:    "https://fakeuser@www.webtoons.com/en/fantasy/tower/list?title_no=95",
		},
		{
			expectErr: webtoon.ErrInvalidURL,
			name:      "non-443 port rejected",
			rawURL:    "https://www.webtoons.com:8080/en/fantasy/tower/list?title_no=95",
		},
		{
			expectErr:  nil,
			name:       "explicit 443 port accepted",
			rawURL:     "https://www.webtoons.com:443/en/fantasy/tower/list?title_no=95",
			expectLang: "en",
			expectSlug: "tower",
			expectNo:   95,
			isCanvas:   false,
		},
		{
			expectErr: webtoon.ErrInvalidURL,
			name:      "foreign domain rejected",
			rawURL:    "https://example.com/en/fantasy/tower/list?title_no=95",
		},
		{
			expectErr: webtoon.ErrInvalidURL,
			name:      "missing path segments rejected",
			rawURL:    "https://www.webtoons.com/en/fantasy/tower",
		},
		{
			expectErr: webtoon.ErrInvalidURL,
			name:      "invalid language code rejected",
			rawURL:    "https://www.webtoons.com/1/fantasy/tower/list?title_no=95",
		},
		{
			expectErr: webtoon.ErrInvalidURL,
			name:      "invalid genre rejected",
			rawURL:    "https://www.webtoons.com/en/fantasy^bad/tower/list?title_no=95",
		},
		{
			expectErr: webtoon.ErrInvalidURL,
			name:      "invalid slug rejected",
			rawURL:    "https://www.webtoons.com/en/fantasy/tower^bad/list?title_no=95",
		},
		{
			expectErr: webtoon.ErrInvalidURL,
			name:      "non-list endpoint rejected",
			rawURL:    "https://www.webtoons.com/en/fantasy/tower/viewer?title_no=95",
		},
		{
			expectErr: webtoon.ErrInvalidURL,
			name:      "missing title_no rejected",
			rawURL:    "https://www.webtoons.com/en/fantasy/tower/list",
		},
		{
			expectErr: webtoon.ErrInvalidURL,
			name:      "zero title_no rejected",
			rawURL:    "https://www.webtoons.com/en/fantasy/tower/list?title_no=0",
		},
		{
			expectErr: webtoon.ErrInvalidURL,
			name:      "malformed query escaping rejected",
			rawURL:    "https://www.webtoons.com/en/fantasy/badquery/list?title_no=%zz",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			info, err := webtoon.ParseSeriesURL(tc.rawURL)
			if tc.expectErr != nil {
				if !errors.Is(err, tc.expectErr) {
					t.Fatalf("expected error %v, got %v", tc.expectErr, err)
				}
				if webtoon.IsValidSeriesURL(tc.rawURL) {
					t.Errorf("expected IsValidSeriesURL to be false for %s", tc.rawURL)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !webtoon.IsValidSeriesURL(tc.rawURL) {
				t.Errorf("expected IsValidSeriesURL to be true for %s", tc.rawURL)
			}

			expectedType := "webtoon"
			if tc.isCanvas {
				expectedType = "canvas"
			}

			if info.Language != tc.expectLang {
				t.Errorf("expected lang %s, got %s", tc.expectLang, info.Language)
			}
			if info.SeriesType != expectedType {
				t.Errorf("expected type %s, got %s", expectedType, info.SeriesType)
			}
			if info.TitleSlug != tc.expectSlug {
				t.Errorf("expected slug %s, got %s", tc.expectSlug, info.TitleSlug)
			}
			if info.TitleNo != tc.expectNo {
				t.Errorf("expected title_no %d, got %d", tc.expectNo, info.TitleNo)
			}
		})
	}
}

func mockTransport(req *http.Request) (*http.Response, error) {
	path := req.URL.Path

	switch path {
	case "/en/fantasy/tower/list":
		body := `<html><head><link rel="canonical" href="https://www.webtoons.com/en/fantasy/tower/list?title_no=95"><meta property="og:title" content="Tower of God"></head><body></body></html>`
		return createHTTPResponse(http.StatusOK, "text/html", body), nil

	case "/api/v1/webtoon/95/episodes":
		body := `{"result":{"episodeList":[{"episodeNo":1,"episodeTitle":"Episode\u001b\n1","viewerLink":"/en/fantasy/tower/viewer?episode_no=1"},{"episodeNo":2,"episodeTitle":"Episode 2","viewerLink":"/en/fantasy/tower/viewer?episode_no=2"}]}}`
		return createHTTPResponse(http.StatusOK, "application/json", body), nil

	case "/en/fantasy/tower/viewer":
		body := `<div class="top_carousel"><img data-url="/ads/promo.jpg"></div><div class="viewer_img _img_viewer_area"><img data-url="/img/page1.jpg?type=q90"><img data-url="/img/page2.jpg"></div><div class="bottom_nav"><img data-url="/nav/thumb.jpg"></div>`
		return createHTTPResponse(http.StatusOK, "text/html", body), nil

	case "/img/page1.jpg":
		return createHTTPResponse(http.StatusOK, "image/jpeg", "\xff\xd8\xffimage_data_page_1"), nil

	case "/api/v1/webtoon/999/episodes":
		return createHTTPResponse(http.StatusOK, "application/json", `{"result":{"episodeList":[]}}`), nil

	case "/server_error":
		return createHTTPResponse(http.StatusInternalServerError, "text/plain", "internal server error"), nil

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

func TestClientMetadataAndChapters(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{
		Transport: roundTripFunc(mockTransport),
	}

	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
		webtoon.WithHTTPClient(httpClient),
	)

	ctx := context.Background()

	info, err := client.FetchSeriesMetadata(ctx, "https://www.webtoons.com/en/fantasy/tower/list?title_no=95")
	if err != nil {
		t.Fatalf("unexpected error fetching metadata: %v", err)
	}
	if info.TitleNo != 95 {
		t.Errorf("expected title_no 95, got %d", info.TitleNo)
	}
	if info.TitleName != "Tower of God" {
		t.Errorf("expected title name 'Tower of God', got '%s'", info.TitleName)
	}

	chapters, err := client.FetchChapters(ctx, info)
	if err != nil {
		t.Fatalf("unexpected error fetching chapters: %v", err)
	}
	if len(chapters) != 2 {
		t.Fatalf("expected 2 chapters, got %d", len(chapters))
	}
	if chapters[0].Number != 1 || chapters[0].Title != "Episode 1" {
		t.Errorf("unexpected chapter details: %+v", chapters[0])
	}
}

func TestClientImagesAndErrors(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{
		Transport: roundTripFunc(mockTransport),
	}

	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
		webtoon.WithHTTPClient(httpClient),
	)

	ctx := context.Background()

	images, err := client.FetchChapterImageURLs(ctx, "https://www.webtoons.com/en/fantasy/tower/viewer")
	if err != nil {
		t.Fatalf("unexpected error fetching chapter images: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(images))
	}
	if images[0] != "/img/page1.jpg" {
		t.Errorf("expected stripped image url /img/page1.jpg, got %s", images[0])
	}

	data, err := client.DownloadImage(ctx, "https://www.webtoons.com"+images[0])
	if err != nil {
		t.Fatalf("unexpected error downloading image: %v", err)
	}
	if string(data) != "\xff\xd8\xffimage_data_page_1" {
		t.Errorf("expected image payload, got %s", string(data))
	}

	emptyInfo := &webtoon.SeriesInfo{
		Language:   "en",
		SeriesType: "webtoon",
		TitleNo:    999,
	}
	_, err = client.FetchChapters(ctx, emptyInfo)
	if !errors.Is(err, webtoon.ErrNoEpisodes) {
		t.Errorf("expected ErrNoEpisodes, got %v", err)
	}

	_, err = client.DownloadImage(ctx, "https://www.webtoons.com/not_found")
	if !errors.Is(err, webtoon.ErrUnexpectedStatus) {
		t.Errorf("expected ErrUnexpectedStatus, got %v", err)
	}
}

func TestClientErrorScenarios(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/not_found", "/en/fantasy/not-found/list":
				return createHTTPResponse(http.StatusNotFound, "text/plain", "not found"), nil
			case "/no_images_viewer":
				return createHTTPResponse(http.StatusOK, "text/html", "<div class=\"viewer_img _img_viewer_area\">No images</div>"), nil
			default:
				return mockTransport(req)
			}
		}),
	}

	client := webtoon.NewClient(
		5*time.Second,
		webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
		webtoon.WithHTTPClient(httpClient),
	)

	t.Run("metadata fetch invalid url error", func(t *testing.T) {
		t.Parallel()
		_, err := client.FetchSeriesMetadata(context.Background(), "invalid-series-url")
		if !errors.Is(err, webtoon.ErrInvalidURL) {
			t.Fatalf("expected ErrInvalidURL, got %v", err)
		}
	})

	t.Run("metadata fetch network error", func(t *testing.T) {
		t.Parallel()
		_, err := client.FetchSeriesMetadata(context.Background(), "https://www.webtoons.com/en/fantasy/not-found/list?title_no=404")
		if err == nil {
			t.Fatalf("expected error fetching metadata for 404")
		}
	})

	t.Run("chapters fetch invalid title_no", func(t *testing.T) {
		t.Parallel()
		zeroInfo := &webtoon.SeriesInfo{TitleNo: 0}
		_, err := client.FetchChapters(context.Background(), zeroInfo)
		if !errors.Is(err, webtoon.ErrUnexpectedStatus) {
			t.Fatalf("expected ErrUnexpectedStatus, got %v", err)
		}
	})

	t.Run("chapter images fetch no images", func(t *testing.T) {
		t.Parallel()
		_, err := client.FetchChapterImageURLs(context.Background(), "https://www.webtoons.com/no_images_viewer")
		if !errors.Is(err, webtoon.ErrNoImagesFound) {
			t.Fatalf("expected ErrNoImagesFound, got %v", err)
		}
	})

	t.Run("pre-canceled context in executeWithRetry", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := client.DownloadImage(ctx, "https://www.webtoons.com/img/page1.jpg")
		if err == nil {
			t.Fatalf("expected context canceled error")
		}
	})

	t.Run("non-image payload rejected in DownloadImage", func(t *testing.T) {
		t.Parallel()
		_, err := client.DownloadImage(context.Background(), "https://www.webtoons.com/no_images_viewer")
		if !errors.Is(err, webtoon.ErrInvalidImage) {
			t.Fatalf("expected ErrInvalidImage, got %v", err)
		}
	})
}
