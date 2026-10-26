// Package zip provides CBZ and standard ZIP archive generation for comic chapters.
package zip

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/underhax/go-webtoons-downloader/internal/validator"
	"github.com/underhax/go-webtoons-downloader/internal/writer"
)

// Writer packages chapter images into a zip or cbz archive.
type Writer struct {
	extension string
}

// NewCBZ creates a chapter writer producing .cbz archives.
func NewCBZ() *Writer {
	return &Writer{extension: ".cbz"}
}

// NewZIP creates a chapter writer producing .zip archives.
func NewZIP() *Writer {
	return &Writer{extension: ".zip"}
}

// Extension returns the archive file extension (.cbz or .zip).
func (w *Writer) Extension() string {
	return w.extension
}

func defaultFileSync(f *os.File) error {
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync file: %w", err)
	}
	return nil
}

func defaultFileClose(f *os.File) error {
	if err := f.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}
	return nil
}

func defaultWriteComicInfo(zw *zip.Writer, meta *writer.ChapterMetadata) error {
	return writeComicInfo(zw, meta)
}

func defaultNewZipWriter(w io.Writer) *zip.Writer {
	return zip.NewWriter(w)
}

var (
	osMkdirAll          = os.MkdirAll
	osOpenFile          = os.OpenFile
	osRemove            = os.Remove
	osRename            = os.Rename
	fileSync            = defaultFileSync
	fileClose           = defaultFileClose
	executeZipWriteFunc = defaultExecuteZipWrite
	writeComicInfoFunc  = defaultWriteComicInfo
	buildComicInfoFunc  = writer.BuildComicInfoXML
	newZipWriter        = defaultNewZipWriter
)

// WriteChapter saves chapter pages into an archive atomically with strict file permissions.
func (w *Writer) WriteChapter(ctx context.Context, destPath string, pages []writer.Page, meta *writer.ChapterMetadata) error {
	if len(pages) == 0 {
		return writer.ErrEmptyPages
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context canceled before zip write: %w", err)
	}

	if _, err := validator.ValidateWriteTarget(destPath); err != nil {
		return fmt.Errorf("validate zip destination: %w", err)
	}

	cleanDest := filepath.Clean(destPath)
	dir := filepath.Dir(cleanDest)
	if err := osMkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create zip destination directory: %w", err)
	}

	tmpPath := filepath.Clean(cleanDest + ".tmp")
	file, err := osOpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open tmp zip file: %w", err)
	}

	writeErr := executeZipWriteFunc(ctx, file, tmpPath, cleanDest, pages, meta)
	if writeErr != nil {
		if rerr := osRemove(tmpPath); rerr != nil && !os.IsNotExist(rerr) {
			return errors.Join(writeErr, fmt.Errorf("remove tmp zip: %w", rerr))
		}
		return writeErr
	}

	return nil
}

func defaultExecuteZipWrite(ctx context.Context, file *os.File, tmpPath, cleanDest string, pages []writer.Page, meta *writer.ChapterMetadata) (retErr error) {
	defer func() {
		if cerr := fileClose(file); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
			retErr = errors.Join(retErr, fmt.Errorf("close zip file in defer: %w", cerr))
		}
	}()

	zw := newZipWriter(file)
	if meta != nil {
		if err := writeComicInfoFunc(zw, meta); err != nil {
			return err
		}
	}

	for _, page := range pages {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context canceled during archive assembly: %w", err)
		}

		pageName := fmt.Sprintf("%03d.jpg", page.Index)
		entry, err := zw.Create(pageName)
		if err != nil {
			return fmt.Errorf("create archive entry %s: %w", pageName, err)
		}

		if _, err := entry.Write(page.Data); err != nil {
			return fmt.Errorf("write archive entry %s: %w", pageName, err)
		}
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("close zip writer: %w", err)
	}

	if err := fileSync(file); err != nil {
		return fmt.Errorf("sync zip file: %w", err)
	}

	if err := fileClose(file); err != nil {
		return fmt.Errorf("close zip file: %w", err)
	}

	if err := osRename(tmpPath, cleanDest); err != nil {
		return fmt.Errorf("atomic rename archive: %w", err)
	}

	return nil
}

func writeComicInfo(zw *zip.Writer, meta *writer.ChapterMetadata) error {
	entry, err := zw.Create("ComicInfo.xml")
	if err != nil {
		return fmt.Errorf("create comicinfo entry: %w", err)
	}

	data, err := buildComicInfoFunc(meta)
	if err != nil {
		return fmt.Errorf("marshal comicinfo xml: %w", err)
	}

	if _, err := entry.Write(data); err != nil {
		return fmt.Errorf("write comicinfo xml: %w", err)
	}

	return nil
}
