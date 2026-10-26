// Package raw provides raw image directory persistence for comic chapters.
package raw

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/underhax/go-webtoons-downloader/internal/validator"
	"github.com/underhax/go-webtoons-downloader/internal/writer"
)

// Writer writes chapter pages as individual images inside a folder.
type Writer struct{}

// New creates a new instance of raw folder writer.
func New() *Writer {
	return &Writer{}
}

// Extension returns an empty extension representing a directory target.
func (w *Writer) Extension() string {
	return ""
}

func defaultWriteComicInfoFile(cleanDir string, meta *writer.ChapterMetadata) error {
	data, err := buildComicInfoFunc(meta)
	if err != nil {
		return fmt.Errorf("marshal comicinfo xml: %w", err)
	}

	filePath := filepath.Clean(filepath.Join(cleanDir, "ComicInfo.xml"))
	if err := osWriteFile(filePath, data, 0o600); err != nil {
		return fmt.Errorf("write comicinfo file: %w", err)
	}

	return nil
}

var (
	osMkdirAll             = os.MkdirAll
	osWriteFile            = os.WriteFile
	buildComicInfoFunc     = writer.BuildComicInfoXML
	writeComicInfoFileFunc = defaultWriteComicInfoFile
)

// WriteChapter saves individual page images into the specified chapter directory with strict permissions.
func (w *Writer) WriteChapter(ctx context.Context, destPath string, pages []writer.Page, meta *writer.ChapterMetadata) error {
	if len(pages) == 0 {
		return writer.ErrEmptyPages
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context canceled before raw write: %w", err)
	}

	if _, err := validator.ValidateWriteTarget(destPath); err != nil {
		return fmt.Errorf("validate raw destination: %w", err)
	}

	cleanDir := filepath.Clean(destPath)
	if err := osMkdirAll(cleanDir, 0o700); err != nil {
		return fmt.Errorf("create raw chapter directory: %w", err)
	}

	for _, page := range pages {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context canceled during raw page saving: %w", err)
		}

		pageName := fmt.Sprintf("%03d.jpg", page.Index)
		filePath := filepath.Clean(filepath.Join(cleanDir, pageName))

		if err := osWriteFile(filePath, page.Data, 0o600); err != nil {
			return fmt.Errorf("write raw page file %s: %w", pageName, err)
		}
	}

	if meta != nil {
		if err := writeComicInfoFileFunc(cleanDir, meta); err != nil {
			return err
		}
	}

	return nil
}
