package raw_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/underhax/go-webtoons-downloader/internal/writer"
	rawwriter "github.com/underhax/go-webtoons-downloader/internal/writer/raw"
)

func TestRawWriterTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expectErr  error
		name       string
		pages      []writer.Page
		cancelCtx  bool
		expectPerm os.FileMode
	}{
		{
			expectErr:  nil,
			name:       "valid multiple raw pages",
			pages:      []writer.Page{{Data: []byte("img1"), Index: 1}, {Data: []byte("img2"), Index: 2}},
			cancelCtx:  false,
			expectPerm: 0o600,
		},
		{
			expectErr:  writer.ErrEmptyPages,
			name:       "empty pages rejected",
			pages:      []writer.Page{},
			cancelCtx:  false,
			expectPerm: 0,
		},
		{
			expectErr:  context.Canceled,
			name:       "canceled context halts write",
			pages:      []writer.Page{{Data: []byte("img"), Index: 1}},
			cancelCtx:  true,
			expectPerm: 0,
		},
	}

	w := rawwriter.New()
	if w.Extension() != "" {
		t.Errorf("expected empty extension, got %s", w.Extension())
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			destPath := filepath.Clean(filepath.Join(tempDir, "001"))

			ctx, cancel := context.WithCancel(context.Background())
			if tc.cancelCtx {
				cancel()
			} else {
				defer cancel()
			}

			err := w.WriteChapter(ctx, destPath, tc.pages, &writer.ChapterMetadata{
				SeriesTitle:   "Tower",
				ChapterTitle:  "Episode 1",
				ChapterNumber: 1,
			})
			if tc.expectErr != nil {
				if !errors.Is(err, tc.expectErr) {
					t.Fatalf("expected error %v, got %v", tc.expectErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, page := range tc.pages {
				filePath := filepath.Clean(filepath.Join(destPath, "001.jpg"))
				if page.Index == 2 {
					filePath = filepath.Clean(filepath.Join(destPath, "002.jpg"))
				}

				info, statErr := os.Stat(filePath)
				if statErr != nil {
					t.Fatalf("expected file to exist: %v", statErr)
				}

				if info.Mode().Perm() != tc.expectPerm {
					t.Errorf("expected file mode %v, got %v", tc.expectPerm, info.Mode().Perm())
				}
			}

			comicInfoPath := filepath.Clean(filepath.Join(destPath, "ComicInfo.xml"))
			info, statErr := os.Stat(comicInfoPath)
			if statErr != nil {
				t.Fatalf("expected ComicInfo.xml to exist: %v", statErr)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("expected ComicInfo.xml mode 0600, got %v", info.Mode().Perm())
			}
			data, readErr := os.ReadFile(comicInfoPath)
			if readErr != nil {
				t.Fatalf("failed to read ComicInfo.xml: %v", readErr)
			}
			if !strings.Contains(string(data), "<Series>Tower</Series>") {
				t.Errorf("expected Series tag in ComicInfo.xml, got: %s", string(data))
			}
		})
	}
}

func TestRawWriterErrors(t *testing.T) {
	t.Parallel()

	w := rawwriter.New()

	t.Run("mkdir failure when parent is file", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		blockedFile := filepath.Join(tempDir, "blocked")
		if err := os.WriteFile(blockedFile, []byte("data"), 0o600); err != nil {
			t.Fatalf("failed to create blocked file: %v", err)
		}

		destPath := filepath.Join(blockedFile, "chapter")
		err := w.WriteChapter(context.Background(), destPath, []writer.Page{{Data: []byte("img"), Index: 1}}, nil)
		if err == nil {
			t.Fatalf("expected error when mkdir target parent is a file")
		}
	})

	t.Run("write file failure when page file is directory", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "chapter")
		if err := os.MkdirAll(filepath.Join(destPath, "001.jpg"), 0o700); err != nil {
			t.Fatalf("failed to create dummy page directory: %v", err)
		}

		err := w.WriteChapter(context.Background(), destPath, []writer.Page{{Data: []byte("img"), Index: 1}}, nil)
		if err == nil {
			t.Fatalf("expected error when writing file to existing directory")
		}
	})

	t.Run("canceled context during page iteration", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "chapter_iter")

		ctx, cancel := context.WithCancel(context.Background())
		pages := make([]writer.Page, 50)
		for i := range pages {
			pages[i] = writer.Page{Data: []byte("dummy_image_data"), Index: i + 1}
		}

		time.AfterFunc(100*time.Microsecond, cancel)
		err := w.WriteChapter(ctx, destPath, pages, nil)
		if err == nil {
			t.Fatalf("expected context canceled error during page saving")
		}
	})

	t.Run("invalid write destination rejected", func(t *testing.T) {
		t.Parallel()
		err := w.WriteChapter(context.Background(), "../escape_raw", []writer.Page{{Data: []byte("img"), Index: 1}}, nil)
		if err == nil {
			t.Fatalf("expected error for traversal destination path")
		}
	})
}
