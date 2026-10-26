package zip_test

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/underhax/go-webtoons-downloader/internal/writer"
	zipwriter "github.com/underhax/go-webtoons-downloader/internal/writer/zip"
)

func TestZipWriterTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		writer     writer.ChapterWriter
		expectErr  error
		name       string
		pages      []writer.Page
		expectPerm os.FileMode
		cancelCtx  bool
		isCBZ      bool
	}{
		{
			writer:     zipwriter.NewCBZ(),
			expectErr:  nil,
			name:       "valid cbz with multiple pages",
			pages:      []writer.Page{{Data: []byte("test_data_1"), Index: 1}, {Data: []byte("test_data_2"), Index: 2}},
			cancelCtx:  false,
			expectPerm: 0o600,
			isCBZ:      true,
		},
		{
			writer:     zipwriter.NewZIP(),
			expectErr:  nil,
			name:       "valid standard zip with pages",
			pages:      []writer.Page{{Data: []byte("test_zip_data"), Index: 1}},
			cancelCtx:  false,
			expectPerm: 0o600,
			isCBZ:      false,
		},
		{
			writer:     zipwriter.NewCBZ(),
			expectErr:  writer.ErrEmptyPages,
			name:       "empty pages rejected",
			pages:      []writer.Page{},
			cancelCtx:  false,
			expectPerm: 0,
			isCBZ:      true,
		},
		{
			writer:     zipwriter.NewZIP(),
			expectErr:  context.Canceled,
			name:       "canceled context halts write",
			pages:      []writer.Page{{Data: []byte("data"), Index: 1}},
			cancelCtx:  true,
			expectPerm: 0,
			isCBZ:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ext := tc.writer.Extension()
			if ext == "" {
				t.Fatalf("unexpected empty extension")
			}

			tempDir := t.TempDir()
			destPath := filepath.Clean(filepath.Join(tempDir, "chapter"+ext))

			ctx, cancel := context.WithCancel(context.Background())
			if tc.cancelCtx {
				cancel()
			} else {
				defer cancel()
			}

			err := tc.writer.WriteChapter(ctx, destPath, tc.pages, &writer.ChapterMetadata{
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

			reader, openErr := zip.OpenReader(destPath)
			if openErr != nil {
				t.Fatalf("failed to open created zip archive: %v", openErr)
			}
			defer func() {
				if cerr := reader.Close(); cerr != nil {
					t.Errorf("failed to close zip reader: %v", cerr)
				}
			}()

			expectedFiles := len(tc.pages) + 1
			if len(reader.File) != expectedFiles {
				t.Errorf("expected %d files in zip, found %d", expectedFiles, len(reader.File))
			}

			foundComicInfo := false
			for _, file := range reader.File {
				if file.Name == "ComicInfo.xml" {
					foundComicInfo = true
					break
				}
			}
			if !foundComicInfo {
				t.Errorf("expected ComicInfo.xml in archive %s", destPath)
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

func TestZipWriterErrorsAndBranches(t *testing.T) {
	t.Parallel()

	w := zipwriter.NewCBZ()

	t.Run("mkdir failure when parent is file", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		blockedFile := filepath.Join(tempDir, "file_blocking_dir")
		if err := os.WriteFile(blockedFile, []byte("block"), 0o600); err != nil {
			t.Fatalf("failed to create blocking file: %v", err)
		}

		destPath := filepath.Join(blockedFile, "sub", "chapter.cbz")
		err := w.WriteChapter(context.Background(), destPath, []writer.Page{{Data: []byte("img"), Index: 1}}, nil)
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

		destPath := filepath.Join(readOnlyDir, "chapter.cbz")
		err := w.WriteChapter(context.Background(), destPath, []writer.Page{{Data: []byte("img"), Index: 1}}, nil)
		if err == nil {
			t.Fatalf("expected error when destination directory is read-only")
		}
	})

	t.Run("atomic rename failure when destination is directory", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "existing_dir_target.cbz")
		if err := os.Mkdir(destPath, 0o700); err != nil {
			t.Fatalf("failed to create dir target: %v", err)
		}

		err := w.WriteChapter(context.Background(), destPath, []writer.Page{{Data: []byte("img"), Index: 1}}, nil)
		if err == nil {
			t.Fatalf("expected error when renaming over an existing directory")
		}
	})

	t.Run("context canceled during archive assembly", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "assembly_cancel.cbz")

		ctx := &stepCancelContext{
			Context:     context.Background(),
			cancelAfter: 1,
		}
		pages := []writer.Page{
			{Data: []byte("archive_image_bytes"), Index: 1},
			{Data: []byte("archive_image_bytes"), Index: 2},
		}

		err := w.WriteChapter(ctx, destPath, pages, nil)
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled error during assembly, got %v", err)
		}
	})

	t.Run("cbz without metadata and with chapter number zero", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()

		destNoMeta := filepath.Join(tempDir, "nometa.cbz")
		if err := w.WriteChapter(context.Background(), destNoMeta, []writer.Page{{Data: []byte("img"), Index: 1}}, nil); err != nil {
			t.Fatalf("unexpected error for cbz without metadata: %v", err)
		}

		destZeroNum := filepath.Join(tempDir, "zero_chapter.cbz")
		metaZero := &writer.ChapterMetadata{
			SeriesTitle:   "Series Zero",
			ChapterNumber: 0,
		}
		if err := w.WriteChapter(context.Background(), destZeroNum, []writer.Page{{Data: []byte("img"), Index: 1}}, metaZero); err != nil {
			t.Fatalf("unexpected error for cbz with chapter number 0: %v", err)
		}
	})

	t.Run("invalid write destination rejected", func(t *testing.T) {
		t.Parallel()
		err := w.WriteChapter(context.Background(), "../escape.cbz", []writer.Page{{Data: []byte("img"), Index: 1}}, nil)
		if err == nil {
			t.Fatalf("expected error for traversal destination path")
		}
	})
}
