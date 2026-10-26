package webtoon_test

import (
	"errors"
	"testing"

	"github.com/underhax/go-webtoons-downloader/internal/webtoon"
)

func TestExtractSeriesTitleTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		html         string
		fallbackSlug string
		expected     string
	}{
		{
			name:         "og:title with apostrophe in double quotes",
			html:         `<html><head><meta property="og:title" content="Nebula's Civilization"></head></html>`,
			fallbackSlug: "slug-one",
			expected:     "Nebula's Civilization",
		},
		{
			name:         "og:title with single quotes and html entity",
			html:         `<html><head><meta property='og:title' content='Hero&rsquo;s Return'></head></html>`,
			fallbackSlug: "slug-two",
			expected:     "Hero’s Return",
		},
		{
			name:         "og:title content attribute before property",
			html:         `<html><head><meta content="Player's Destiny" property="og:title"></head></html>`,
			fallbackSlug: "slug-three",
			expected:     "Player's Destiny",
		},
		{
			name:         "fallback to h1 subj",
			html:         `<html><body><h1 class="subj">Knight's Tale</h1></body></html>`,
			fallbackSlug: "slug-four",
			expected:     "Knight's Tale",
		},
		{
			name:         "fallback to h3 subj with html entity",
			html:         `<html><body><h3 class="subj">The Extra&rsquo;s Academy</h3></body></html>`,
			fallbackSlug: "slug-five",
			expected:     "The Extra’s Academy",
		},
		{
			name:         "fallback to slug when no markup matches",
			html:         `<html><body><div class="unrelated">Unknown</div></body></html>`,
			fallbackSlug: "fallback-slug",
			expected:     "fallback-slug",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := webtoon.ExtractSeriesTitle(tc.html, tc.fallbackSlug)
			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestExtractTitleNoTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expectedErr error
		name        string
		html        string
		expected    int
	}{
		{
			expectedErr: nil,
			name:        "valid canonical title_no",
			html:        `<link rel="canonical" href="https://example.com/en/fantasy/test/list?title_no=6535">`,
			expected:    6535,
		},
		{
			expectedErr: webtoon.ErrTitleNoNotFound,
			name:        "canonical without title_no",
			html:        `<link rel="canonical" href="https://example.com/en/fantasy/test/list">`,
			expected:    0,
		},
		{
			expectedErr: webtoon.ErrTitleNoNotFound,
			name:        "canonical with non-numeric title_no",
			html:        `<link rel="canonical" href="https://example.com/en/fantasy/test/list?title_no=abc">`,
			expected:    0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := webtoon.ExtractTitleNo(tc.html)
			if tc.expectedErr != nil {
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res != tc.expected {
				t.Errorf("expected %d, got %d", tc.expected, res)
			}
		})
	}
}

func TestExtractImageURLsTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expectedErr error
		name        string
		html        string
		expected    []string
	}{
		{
			expectedErr: nil,
			name:        "viewer area with multiple images and query strip",
			html:        `<div class="viewer_img _img_viewer_area"><img data-url="https://example.com/1.jpg?type=q90"><img data-url="https://example.org/2.jpg"></div>`,
			expected:    []string{"https://example.com/1.jpg", "https://example.org/2.jpg"},
		},
		{
			expectedErr: nil,
			name:        "viewer box container id",
			html:        `<div id="_viewerBox"><img data-url="https://example.net/1.jpg"></div>`,
			expected:    []string{"https://example.net/1.jpg"},
		},
		{
			expectedErr: webtoon.ErrNoImagesFound,
			name:        "no viewer container found",
			html:        `<div class="other_area"><img data-url="https://example.com/1.jpg"></div>`,
			expected:    nil,
		},
		{
			expectedErr: webtoon.ErrNoImagesFound,
			name:        "viewer container without img tags",
			html:        `<div class="_img_viewer_area"><p>No images</p></div>`,
			expected:    nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := webtoon.ExtractImageURLs(tc.html)
			if tc.expectedErr != nil {
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(res) != len(tc.expected) {
				t.Fatalf("expected %d urls, got %d", len(tc.expected), len(res))
			}
			for i, u := range res {
				if u != tc.expected[i] {
					t.Errorf("index %d: expected %s, got %s", i, tc.expected[i], u)
				}
			}
		})
	}
}

func TestExtractMetadataTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		html            string
		expectedGenre   string
		expectedAuthor  string
		expectedSummary string
	}{
		{
			name: "full detail header with genre authors and summary",
			html: `<div class="detail_header">
				<h2 class="genre g_fantasy">Fantasy</h2>
				<h1 class="subj">Nebula's Civilization</h1>
				<div class="author_area">Wirae , Beomgwin <button>info</button></div>
			</div>
			<p class="summary">A thrilling tale of civilization &amp; gods.</p>`,
			expectedGenre:   "Fantasy",
			expectedAuthor:  "Wirae, Beomgwin",
			expectedSummary: "A thrilling tale of civilization & gods.",
		},
		{
			name: "meta tags for author and description",
			html: `<meta name="author" content="SIU">
			<meta property="og:description" content="Reach the top of the Tower.">
			<h2 class="genre">Action</h2>`,
			expectedGenre:   "Action",
			expectedAuthor:  "SIU",
			expectedSummary: "Reach the top of the Tower.",
		},
		{
			name: "alternative meta attributes for creator and description",
			html: `<meta name="author" content='Creator Name'>
			<meta name="description" content='Alternative summary text.'>`,
			expectedGenre:   "",
			expectedAuthor:  "Creator Name",
			expectedSummary: "Alternative summary text.",
		},
		{
			name:            "empty html returns empty strings",
			html:            `<div>Empty</div>`,
			expectedGenre:   "",
			expectedAuthor:  "",
			expectedSummary: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := webtoon.ExtractGenre(tc.html)
			if g != tc.expectedGenre {
				t.Errorf("expected genre %q, got %q", tc.expectedGenre, g)
			}

			a := webtoon.ExtractAuthor(tc.html)
			if a != tc.expectedAuthor {
				t.Errorf("expected author %q, got %q", tc.expectedAuthor, a)
			}

			s := webtoon.ExtractSummary(tc.html)
			if s != tc.expectedSummary {
				t.Errorf("expected summary %q, got %q", tc.expectedSummary, s)
			}
		})
	}
}
