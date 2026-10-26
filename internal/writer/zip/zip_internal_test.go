package zip

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/underhax/go-webtoons-downloader/internal/writer"
)

type failWriteCloser struct{}

func (f *failWriteCloser) Write(_ []byte) (int, error) {
	return 0, errors.New("simulated write failure")
}

func (f *failWriteCloser) Close() error {
	return nil
}

type failOnCloseWriter struct{}

func (f *failOnCloseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (f *failOnCloseWriter) Close() error {
	return errors.New("simulated compressor close failure")
}

func mockFailingCompressorWriter(_ io.Writer) (io.WriteCloser, error) {
	return &failWriteCloser{}, nil
}

func mockFailingCompressorInit(_ io.Writer) (io.WriteCloser, error) {
	return nil, errors.New("simulated init compressor failure")
}

func mockFailingCompressorClose(_ io.Writer) (io.WriteCloser, error) {
	return &failOnCloseWriter{}, nil
}

func mockFailingBuildComicInfo(_ *writer.ChapterMetadata) ([]byte, error) {
	return nil, errors.New("simulated marshal comicinfo xml failure")
}

func mockNewZipWriterInitFail(w io.Writer) *zip.Writer {
	zw := zip.NewWriter(w)
	zw.RegisterCompressor(zip.Deflate, mockFailingCompressorInit)
	return zw
}

func mockNewZipWriterWriteFail(w io.Writer) *zip.Writer {
	zw := zip.NewWriter(w)
	zw.RegisterCompressor(zip.Deflate, mockFailingCompressorWriter)
	return zw
}

func mockNewZipWriterCloseFail(w io.Writer) *zip.Writer {
	zw := zip.NewWriter(w)
	zw.RegisterCompressor(zip.Deflate, mockFailingCompressorClose)
	return zw
}

func mockFailingSync(_ *os.File) error {
	return errors.New("simulated sync failure")
}

func mockFailingClose(_ *os.File) error {
	return errors.New("simulated close failure")
}

func mockFailingRename(_, _ string) error {
	return errors.New("simulated rename failure")
}

func mockFailingExecuteWrite(_ context.Context, _ *os.File, _, _ string, _ []writer.Page, _ *writer.ChapterMetadata) error {
	return errors.New("simulated write execution failure")
}

func mockFailingWriteComicInfo(_ *zip.Writer, _ *writer.ChapterMetadata) error {
	return errors.New("simulated comic info write failure")
}

func mockFailingRemove(_ string) error {
	return errors.New("simulated remove failure")
}

func mockFailingMkdirAll(_ string, _ os.FileMode) error {
	return errors.New("simulated mkdir failure")
}

func TestDefaultZipFileOperations(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp(t.TempDir(), "dummy-zip-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}

	if err := defaultFileSync(f); err != nil {
		t.Errorf("expected successful sync, got %v", err)
	}

	if err := defaultFileClose(f); err != nil {
		t.Errorf("expected successful close, got %v", err)
	}

	if err := defaultFileSync(f); err == nil {
		t.Errorf("expected error syncing closed file, got nil")
	}
}

func TestDefaultXMLMarshalAndComicInfo(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	zw := defaultNewZipWriter(&buf)
	if zw == nil {
		t.Fatal("expected non-nil zip writer from default constructor")
	}

	meta := &writer.ChapterMetadata{SeriesTitle: "AlphaSeries", ChapterNumber: 1}
	if err := defaultWriteComicInfo(zw, meta); err != nil {
		t.Errorf("expected successful comic info write, got %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
}

func TestZipWriterSyncAndCloseErrors(t *testing.T) {
	origSync := fileSync
	origClose := fileClose
	defer func() {
		fileSync = origSync
		fileClose = origClose
	}()

	w := NewCBZ()
	pages := []writer.Page{{Data: []byte("img"), Index: 1}}

	t.Run("file sync failure", func(t *testing.T) {
		tempDir := t.TempDir()
		dest := filepath.Join(tempDir, "sync_err.cbz")

		fileSync = mockFailingSync
		err := w.WriteChapter(context.Background(), dest, pages, nil)
		fileSync = origSync

		if err == nil || !strings.Contains(err.Error(), "sync zip file") {
			t.Errorf("expected sync error, got: %v", err)
		}
	})

	t.Run("file close failure", func(t *testing.T) {
		tempDir := t.TempDir()
		dest := filepath.Join(tempDir, "close_err.cbz")

		fileClose = mockFailingClose
		err := w.WriteChapter(context.Background(), dest, pages, nil)
		fileClose = origClose

		if err == nil || !strings.Contains(err.Error(), "close zip file") {
			t.Errorf("expected close error, got: %v", err)
		}
	})
}

func TestZipWriterJoinedRemoveAndRenameErrors(t *testing.T) {
	origExecute := executeZipWriteFunc
	origRemove := osRemove
	origRename := osRename
	defer func() {
		executeZipWriteFunc = origExecute
		osRemove = origRemove
		osRename = origRename
	}()

	w := NewCBZ()
	pages := []writer.Page{{Data: []byte("img"), Index: 1}}

	t.Run("joined remove error", func(t *testing.T) {
		tempDir := t.TempDir()
		dest := filepath.Join(tempDir, "remove_err.cbz")

		executeZipWriteFunc = mockFailingExecuteWrite
		osRemove = mockFailingRemove
		err := w.WriteChapter(context.Background(), dest, pages, nil)
		executeZipWriteFunc = origExecute
		osRemove = origRemove

		if err == nil || !strings.Contains(err.Error(), "remove tmp zip") {
			t.Errorf("expected joined remove error, got: %v", err)
		}
	})

	t.Run("rename error", func(t *testing.T) {
		tempDir := t.TempDir()
		dest := filepath.Join(tempDir, "rename_err.cbz")

		osRename = mockFailingRename
		err := w.WriteChapter(context.Background(), dest, pages, nil)
		osRename = origRename

		if err == nil || !strings.Contains(err.Error(), "atomic rename archive") {
			t.Errorf("expected rename error, got: %v", err)
		}
	})

	t.Run("mkdir error", func(t *testing.T) {
		tempDir := t.TempDir()
		dest := filepath.Join(tempDir, "mkdir_err.cbz")

		origMkdir := osMkdirAll
		osMkdirAll = mockFailingMkdirAll
		err := w.WriteChapter(context.Background(), dest, pages, nil)
		osMkdirAll = origMkdir

		if err == nil || !strings.Contains(err.Error(), "create zip destination directory") {
			t.Errorf("expected create zip destination directory error, got: %v", err)
		}
	})
}

func TestZipWriterComicInfoErrorInExecute(t *testing.T) {
	origComicInfo := writeComicInfoFunc
	defer func() {
		writeComicInfoFunc = origComicInfo
	}()

	tempDir := t.TempDir()
	dest := filepath.Join(tempDir, "comic_info_err.cbz")
	pages := []writer.Page{{Data: []byte("img"), Index: 1}}
	meta := &writer.ChapterMetadata{SeriesTitle: "BetaSeries"}

	writeComicInfoFunc = mockFailingWriteComicInfo
	w := NewCBZ()
	err := w.WriteChapter(context.Background(), dest, pages, meta)

	if err == nil || !strings.Contains(err.Error(), "simulated comic info write failure") {
		t.Errorf("expected comic info write error, got: %v", err)
	}
}

func TestZipWriterContextCanceledDuringLoop(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pages := []writer.Page{{Data: []byte("img"), Index: 1}}
	tempDir := t.TempDir()
	f, err := os.CreateTemp(tempDir, "cancel-loop-*")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
			t.Logf("close file in test cleanup: %v", cerr)
		}
	}()

	err = defaultExecuteZipWrite(ctx, f, f.Name(), filepath.Join(tempDir, "cancel.zip"), pages, nil)
	if err == nil || !strings.Contains(err.Error(), "context canceled during archive assembly") {
		t.Errorf("expected context canceled during assembly, got: %v", err)
	}
}

func TestWriteComicInfoErrors(t *testing.T) {
	origBuild := buildComicInfoFunc
	defer func() {
		buildComicInfoFunc = origBuild
	}()

	meta := &writer.ChapterMetadata{SeriesTitle: "GammaSeries", ChapterNumber: 10}

	t.Run("create entry failure", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		zw.RegisterCompressor(zip.Deflate, mockFailingCompressorInit)

		err := writeComicInfo(zw, meta)
		if err == nil || !strings.Contains(err.Error(), "create comicinfo entry") {
			t.Errorf("expected create comicinfo entry error, got: %v", err)
		}
	})

	t.Run("marshal xml failure", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)

		buildComicInfoFunc = mockFailingBuildComicInfo
		err := writeComicInfo(zw, meta)
		buildComicInfoFunc = origBuild

		if err == nil || !strings.Contains(err.Error(), "marshal comicinfo xml") {
			t.Errorf("expected marshal comicinfo xml error, got: %v", err)
		}
	})

	t.Run("write entry failure", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		zw.RegisterCompressor(zip.Deflate, mockFailingCompressorWriter)

		err := writeComicInfo(zw, meta)
		if err == nil || !strings.Contains(err.Error(), "write comicinfo xml") {
			t.Errorf("expected write comicinfo xml error, got: %v", err)
		}
	})
}

func TestZipWriterPageWriteErrors(t *testing.T) {
	origNewZip := newZipWriter
	defer func() {
		newZipWriter = origNewZip
	}()

	pages := []writer.Page{{Data: []byte("img"), Index: 1}}

	t.Run("create page entry failure", func(t *testing.T) {
		tempDir := t.TempDir()
		f, err := os.CreateTemp(tempDir, "page-create-err-*")
		if err != nil {
			t.Fatalf("create temp: %v", err)
		}
		defer func() {
			if cerr := f.Close(); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
				t.Logf("close file in test cleanup: %v", cerr)
			}
		}()

		newZipWriter = mockNewZipWriterInitFail
		err = defaultExecuteZipWrite(context.Background(), f, f.Name(), filepath.Join(tempDir, "create_err.zip"), pages, nil)
		newZipWriter = origNewZip

		if err == nil || !strings.Contains(err.Error(), "create archive entry 001.jpg") {
			t.Errorf("expected create entry error, got: %v", err)
		}
	})

	t.Run("write page entry failure", func(t *testing.T) {
		tempDir := t.TempDir()
		f, err := os.CreateTemp(tempDir, "page-write-err-*")
		if err != nil {
			t.Fatalf("create temp: %v", err)
		}
		defer func() {
			if cerr := f.Close(); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
				t.Logf("close file in test cleanup: %v", cerr)
			}
		}()

		newZipWriter = mockNewZipWriterWriteFail
		err = defaultExecuteZipWrite(context.Background(), f, f.Name(), filepath.Join(tempDir, "write_err.zip"), pages, nil)
		newZipWriter = origNewZip

		if err == nil || !strings.Contains(err.Error(), "write archive entry 001.jpg") {
			t.Errorf("expected write entry error, got: %v", err)
		}
	})
}

func TestZipWriterCloseError(t *testing.T) {
	origNewZip := newZipWriter
	defer func() {
		newZipWriter = origNewZip
	}()

	pages := []writer.Page{{Data: []byte("img"), Index: 1}}
	tempDir := t.TempDir()
	f, err := os.CreateTemp(tempDir, "zip-close-err-*")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
			t.Logf("close file in test cleanup: %v", cerr)
		}
	}()

	newZipWriter = mockNewZipWriterCloseFail
	err = defaultExecuteZipWrite(context.Background(), f, f.Name(), filepath.Join(tempDir, "close_err.zip"), pages, nil)

	if err == nil || !strings.Contains(err.Error(), "close zip writer") {
		t.Errorf("expected close zip writer error, got: %v", err)
	}
}

func TestZipWriterDeferCloseError(t *testing.T) {
	origClose := fileClose
	defer func() {
		fileClose = origClose
	}()

	pages := []writer.Page{{Data: []byte("img"), Index: 1}}
	tempDir := t.TempDir()
	f, err := os.CreateTemp(tempDir, "defer-close-*")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fileClose = mockFailingClose
	err = defaultExecuteZipWrite(ctx, f, f.Name(), filepath.Join(tempDir, "out_defer.zip"), pages, nil)

	if err == nil || !strings.Contains(err.Error(), "close zip file in defer") {
		t.Errorf("expected defer close joined error, got %v", err)
	}
}
