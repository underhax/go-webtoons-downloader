package pdf_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/underhax/go-webtoons-downloader/internal/writer"
	"github.com/underhax/go-webtoons-downloader/internal/writer/pdf"
)

func createTestJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("failed to encode test jpeg: %v", err)
	}
	return buf.Bytes()
}

func createTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.RGBA{R: 50, G: 150, B: 250, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode test png: %v", err)
	}
	return buf.Bytes()
}

func TestPDFWriter(t *testing.T) {
	t.Parallel()

	jpegPage1 := createTestJPEG(t, 100, 200)
	jpegPage2 := createTestJPEG(t, 120, 240)
	pngPage := createTestPNG(t, 80, 160)

	tests := []struct {
		expectErr  error
		name       string
		pages      []writer.Page
		cancelCtx  bool
		expectPerm os.FileMode
	}{
		{
			expectErr:  nil,
			name:       "valid multiple jpeg pages",
			pages:      []writer.Page{{Data: jpegPage1, Index: 1}, {Data: jpegPage2, Index: 2}},
			cancelCtx:  false,
			expectPerm: 0o600,
		},
		{
			expectErr:  nil,
			name:       "valid png page normalized to jpeg",
			pages:      []writer.Page{{Data: pngPage, Index: 1}},
			cancelCtx:  false,
			expectPerm: 0o600,
		},
		{
			expectErr:  writer.ErrEmptyPages,
			name:       "empty pages slice",
			pages:      []writer.Page{},
			cancelCtx:  false,
			expectPerm: 0,
		},
		{
			expectErr:  pdf.ErrInvalidImage,
			name:       "corrupt image data",
			pages:      []writer.Page{{Data: []byte("not_an_image"), Index: 1}},
			cancelCtx:  false,
			expectPerm: 0,
		},
		{
			expectErr:  context.Canceled,
			name:       "canceled context",
			pages:      []writer.Page{{Data: jpegPage1, Index: 1}},
			cancelCtx:  true,
			expectPerm: 0,
		},
	}

	w := pdf.New()
	if w.Extension() != ".pdf" {
		t.Errorf("expected extension .pdf, got %s", w.Extension())
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			destPath := filepath.Clean(filepath.Join(tempDir, "chapter.pdf"))

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
				Author:        "Author Name",
				Genre:         "Fantasy",
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

			info, statErr := os.Stat(destPath)
			if statErr != nil {
				t.Fatalf("expected file to exist: %v", statErr)
			}

			if info.Mode().Perm() != tc.expectPerm {
				t.Errorf("expected permissions %v, got %v", tc.expectPerm, info.Mode().Perm())
			}

			content, readErr := os.ReadFile(destPath)
			if readErr != nil {
				t.Fatalf("failed to read pdf content: %v", readErr)
			}

			if !bytes.HasPrefix(content, []byte("%PDF-1.4")) {
				t.Errorf("pdf does not start with magic header %%PDF-1.4")
			}

			if !bytes.HasSuffix(bytes.TrimSpace(content), []byte("%%EOF")) {
				t.Errorf("pdf does not end with %%%%EOF trailer")
			}

			if !bytes.Contains(content, []byte("/Author (Author Name)")) {
				t.Errorf("pdf missing Author metadata")
			}
			if bytes.Contains(content, []byte("/Creator")) {
				t.Errorf("expected no Creator metadata tag")
			}
			if !bytes.Contains(content, []byte("/Producer (webtoons-downloader)")) {
				t.Errorf("pdf missing Producer metadata")
			}
		})
	}
}

type stepCancelContext struct {
	context.Context
	cancelAfter int
	calls       atomic.Int32
}

func (s *stepCancelContext) Err() error {
	if int(s.calls.Add(1)) > s.cancelAfter {
		return context.Canceled
	}
	return s.Context.Err()
}

func (s *stepCancelContext) Done() <-chan struct{} {
	if int(s.calls.Load()) > s.cancelAfter {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return s.Context.Done()
}

func TestPDFWriterErrorsAndMetadata(t *testing.T) {
	t.Parallel()

	w := pdf.New()
	jpegPage := createTestJPEG(t, 50, 50)

	t.Run("mkdir failure when parent is file", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		blockedFile := filepath.Join(tempDir, "file_blocking_dir")
		if err := os.WriteFile(blockedFile, []byte("data"), 0o600); err != nil {
			t.Fatalf("failed to create blocking file: %v", err)
		}

		destPath := filepath.Join(blockedFile, "sub", "chapter.pdf")
		err := w.WriteChapter(context.Background(), destPath, []writer.Page{{Data: jpegPage, Index: 1}}, nil)
		if err == nil {
			t.Fatalf("expected error when parent directory is a file")
		}
	})

	t.Run("open tmp file failure when dir is not writable", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		readOnlyDir := filepath.Join(tempDir, "ro_dir")
		if err := os.Mkdir(readOnlyDir, 0o500); err != nil {
			t.Fatalf("failed to create read-only dir: %v", err)
		}

		destPath := filepath.Join(readOnlyDir, "chapter.pdf")
		err := w.WriteChapter(context.Background(), destPath, []writer.Page{{Data: jpegPage, Index: 1}}, nil)
		if err == nil {
			t.Fatalf("expected error when destination directory is read-only")
		}
	})

	t.Run("atomic rename failure when destination is directory", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "existing_dir_target.pdf")
		if err := os.Mkdir(destPath, 0o700); err != nil {
			t.Fatalf("failed to create dir target: %v", err)
		}

		err := w.WriteChapter(context.Background(), destPath, []writer.Page{{Data: jpegPage, Index: 1}}, nil)
		if err == nil {
			t.Fatalf("expected error when renaming over an existing directory")
		}
	})

	t.Run("context canceled during pdf page loop", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "cancel_loop.pdf")

		ctx := &stepCancelContext{
			Context:     context.Background(),
			cancelAfter: 1,
		}
		pages := []writer.Page{
			{Data: jpegPage, Index: 1},
			{Data: jpegPage, Index: 2},
		}

		err := w.WriteChapter(ctx, destPath, pages, nil)
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled error during page generation, got %v", err)
		}
	})

	t.Run("metadata resolution branches", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()

		destNilMeta := filepath.Join(tempDir, "nil_meta.pdf")
		if err := w.WriteChapter(context.Background(), destNilMeta, []writer.Page{{Data: jpegPage, Index: 1}}, nil); err != nil {
			t.Fatalf("unexpected error with nil metadata: %v", err)
		}

		destEmptyTitleWithNumber := filepath.Join(tempDir, "num_only.pdf")
		metaNum := &writer.ChapterMetadata{
			ChapterNumber: 5,
		}
		if err := w.WriteChapter(context.Background(), destEmptyTitleWithNumber, []writer.Page{{Data: jpegPage, Index: 1}}, metaNum); err != nil {
			t.Fatalf("unexpected error with chapter number only: %v", err)
		}

		destSeriesOnly := filepath.Join(tempDir, "series_only.pdf")
		metaSeries := &writer.ChapterMetadata{
			SeriesTitle: "Tower",
			Author:      "Author (Special \\ Escapes)",
		}
		if err := w.WriteChapter(context.Background(), destSeriesOnly, []writer.Page{{Data: jpegPage, Index: 1}}, metaSeries); err != nil {
			t.Fatalf("unexpected error with series title only: %v", err)
		}
	})

	t.Run("invalid write destination rejected", func(t *testing.T) {
		t.Parallel()
		err := w.WriteChapter(context.Background(), "../escape.pdf", []writer.Page{{Data: jpegPage, Index: 1}}, nil)
		if err == nil {
			t.Fatalf("expected error for traversal destination path")
		}
	})
}
