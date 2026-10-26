package pdf

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/underhax/go-webtoons-downloader/internal/writer"
)

type stepFailWriter struct {
	failOnWrite int
	current     int
}

func (s *stepFailWriter) Write(p []byte) (int, error) {
	s.current++
	if s.current >= s.failOnWrite {
		return 0, errors.New("simulated step write failure")
	}
	return len(p), nil
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

func mockFailingRemove(_ string) error {
	return errors.New("simulated remove failure")
}

func mockFailingMkdirAll(_ string, _ os.FileMode) error {
	return errors.New("simulated mkdir failure")
}

func mockFailingAtomicWrite(_ context.Context, _ *os.File, _, _ string, _ []writer.Page, _ *writer.ChapterMetadata) error {
	return errors.New("simulated atomic write failure")
}

func mockFailingWritePDFDoc(_ context.Context, _ *bufio.Writer, _ []writer.Page, _ *writer.ChapterMetadata) error {
	return errors.New("simulated write pdf document failure")
}

func mockFailingJPEGEncode(_ io.Writer, _ image.Image, _ *jpeg.Options) error {
	return errors.New("simulated jpeg encode failure")
}

func createInternalTestJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 50, 50))
	for y := range 50 {
		for x := range 50 {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("encode internal jpeg: %v", err)
	}
	return buf.Bytes()
}

func createInternalTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 30, 30))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode internal png: %v", err)
	}
	return buf.Bytes()
}

func TestDefaultPDFFileOperations(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp(t.TempDir(), "dummy-pdf-*")
	if err != nil {
		t.Fatalf("create temp: %v", err)
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

func TestDefaultJPEGEncodeAndFail(t *testing.T) {
	origEncode := jpegEncode
	defer func() {
		jpegEncode = origEncode
	}()

	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	var buf bytes.Buffer
	if err := defaultJPEGEncode(&buf, img, nil); err != nil {
		t.Fatalf("expected successful default jpeg encode, got %v", err)
	}

	sfw := &stepFailWriter{failOnWrite: 1}
	if err := defaultJPEGEncode(sfw, img, nil); err == nil {
		t.Errorf("expected error from default jpeg encode with failing writer, got nil")
	}

	pngBytes := createInternalTestPNG(t)
	jpegEncode = mockFailingJPEGEncode
	_, err := extractOrNormalizeImage(pngBytes)
	jpegEncode = origEncode

	if err == nil || !strings.Contains(err.Error(), "encode jpeg") {
		t.Errorf("expected encode jpeg error, got: %v", err)
	}
}

func TestWritePDFDocumentSubcallErrors(t *testing.T) {
	t.Parallel()

	jpegBytes := createInternalTestJPEG(t)
	validPages := []writer.Page{{Data: jpegBytes, Index: 1}}

	t.Run("header failure", func(t *testing.T) {
		sfw := &stepFailWriter{failOnWrite: 1}
		bw := bufio.NewWriterSize(sfw, 1)
		err := writePDFDocument(context.Background(), bw, validPages, nil)
		if err == nil || !strings.Contains(err.Error(), "write pdf header") {
			t.Errorf("expected header write error, got: %v", err)
		}
	})

	t.Run("page failure with corrupt data", func(t *testing.T) {
		var buf bytes.Buffer
		bw := bufio.NewWriter(&buf)
		corruptPages := []writer.Page{{Data: []byte("not_an_image"), Index: 1}}
		err := writePDFDocument(context.Background(), bw, corruptPages, nil)
		if err == nil || !strings.Contains(err.Error(), "process page 1 image") {
			t.Errorf("expected page process error, got: %v", err)
		}
	})

	t.Run("info object failure", func(t *testing.T) {
		sfw := &stepFailWriter{failOnWrite: 9}
		bw := bufio.NewWriterSize(sfw, 1)
		err := writePDFDocument(context.Background(), bw, validPages, nil)
		if err == nil || !strings.Contains(err.Error(), "write info object") {
			t.Errorf("expected info object error, got: %v", err)
		}
	})

	t.Run("xref and trailer failure", func(t *testing.T) {
		sfw := &stepFailWriter{failOnWrite: 10}
		bw := bufio.NewWriterSize(sfw, 1)
		err := writePDFDocument(context.Background(), bw, validPages, nil)
		if err == nil || !strings.Contains(err.Error(), "write xref header") {
			t.Errorf("expected xref error, got: %v", err)
		}
	})
}

func TestDefaultWritePDFDocument(t *testing.T) {
	t.Parallel()

	jpegBytes := createInternalTestJPEG(t)
	pages := []writer.Page{{Data: jpegBytes, Index: 1}}
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)

	err := defaultWritePDFDocument(context.Background(), bw, pages, nil)
	if err != nil {
		t.Fatalf("expected successful write pdf doc, got %v", err)
	}
}

func TestCountingWriterFailure(t *testing.T) {
	t.Parallel()

	sfw := &stepFailWriter{failOnWrite: 1}
	bw := bufio.NewWriterSize(sfw, 1)
	cw := &countingWriter{writer: bw}

	_, err := cw.Write([]byte("hello"))
	if err == nil || !strings.Contains(err.Error(), "counting writer write") {
		t.Errorf("expected counting writer write error, got %v", err)
	}
}

func TestWritePDFHeaderAndCatalogFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expectSubstr string
		failOnWrite  int
	}{
		{failOnWrite: 1, expectSubstr: "write pdf header"},
		{failOnWrite: 2, expectSubstr: "write catalog object"},
		{failOnWrite: 3, expectSubstr: "write pages root object"},
	}

	for _, tc := range tests {
		offsets := make([]int64, 10)
		sfw := &stepFailWriter{failOnWrite: tc.failOnWrite}
		bw := bufio.NewWriterSize(sfw, 1)
		cw := &countingWriter{writer: bw}

		err := writePDFHeaderAndCatalog(cw, offsets, 1)
		if err == nil || !strings.Contains(err.Error(), tc.expectSubstr) {
			t.Errorf("step %d: expected %q error, got %v", tc.failOnWrite, tc.expectSubstr, err)
		}
	}
}

func TestWriteInfoObjectFailures(t *testing.T) {
	t.Parallel()

	offsets := make([]int64, 10)
	sfw := &stepFailWriter{failOnWrite: 1}
	bw := bufio.NewWriterSize(sfw, 1)
	cw := &countingWriter{writer: bw}
	meta := &writer.ChapterMetadata{SeriesTitle: "Tower"}

	err := writeInfoObject(cw, offsets, 3, meta)
	if err == nil || !strings.Contains(err.Error(), "write info object") {
		t.Errorf("expected write info object error, got %v", err)
	}
}

func TestWriteXrefAndTrailerFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expectSubstr string
		failOnWrite  int
	}{
		{failOnWrite: 1, expectSubstr: "write xref header"},
		{failOnWrite: 2, expectSubstr: "write xref entry 1"},
		{failOnWrite: 3, expectSubstr: "write trailer"},
	}

	for _, tc := range tests {
		offsets := make([]int64, 10)
		sfw := &stepFailWriter{failOnWrite: tc.failOnWrite}
		bw := bufio.NewWriterSize(sfw, 1)
		cw := &countingWriter{writer: bw}

		err := writeXrefAndTrailer(cw, offsets, 1, 2)
		if err == nil || !strings.Contains(err.Error(), tc.expectSubstr) {
			t.Errorf("step %d: expected %q error, got %v", tc.failOnWrite, tc.expectSubstr, err)
		}
	}
}

func TestWritePageObjectsFailures(t *testing.T) {
	t.Parallel()

	t.Run("invalid image data", func(t *testing.T) {
		offsets := make([]int64, 10)
		var buf bytes.Buffer
		bw := bufio.NewWriter(&buf)
		cw := &countingWriter{writer: bw}

		err := writePageObjects(cw, writer.Page{Data: []byte("corrupt"), Index: 1}, 1, offsets)
		if err == nil || !strings.Contains(err.Error(), "process page 1 image") {
			t.Errorf("expected invalid image error, got: %v", err)
		}
	})

	jpegBytes := createInternalTestJPEG(t)
	page := writer.Page{Data: jpegBytes, Index: 1}

	tests := []struct {
		expectSubstr string
		failOnWrite  int
	}{
		{failOnWrite: 1, expectSubstr: "write page object 1"},
		{failOnWrite: 2, expectSubstr: "write content object 1"},
		{failOnWrite: 3, expectSubstr: "write image header 1"},
		{failOnWrite: 4, expectSubstr: "write image stream data 1"},
		{failOnWrite: 5, expectSubstr: "write image footer 1"},
	}

	for _, tc := range tests {
		offsets := make([]int64, 10)
		sfw := &stepFailWriter{failOnWrite: tc.failOnWrite}
		bw := bufio.NewWriterSize(sfw, 1)
		cw := &countingWriter{writer: bw}

		err := writePageObjects(cw, page, 1, offsets)
		if err == nil || !strings.Contains(err.Error(), tc.expectSubstr) {
			t.Errorf("step %d: expected %q error, got %v", tc.failOnWrite, tc.expectSubstr, err)
		}
	}
}

func TestExecuteAtomicWriteWriteAndFlushErrors(t *testing.T) {
	origWriteDoc := writePDFDocumentFunc
	defer func() {
		writePDFDocumentFunc = origWriteDoc
	}()

	jpegBytes := createInternalTestJPEG(t)
	pages := []writer.Page{{Data: jpegBytes, Index: 1}}

	t.Run("write pdf doc error", func(t *testing.T) {
		tempDir := t.TempDir()
		f, err := os.CreateTemp(tempDir, "write-doc-err-*")
		if err != nil {
			t.Fatalf("create temp: %v", err)
		}
		defer func() {
			if cerr := f.Close(); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
				t.Logf("cleanup file close: %v", cerr)
			}
		}()

		writePDFDocumentFunc = mockFailingWritePDFDoc
		err = defaultExecuteAtomicWrite(context.Background(), f, f.Name(), filepath.Join(tempDir, "out.pdf"), pages, nil)
		writePDFDocumentFunc = origWriteDoc

		if err == nil || !strings.Contains(err.Error(), "simulated write pdf document failure") {
			t.Errorf("expected write doc error, got: %v", err)
		}
	})

	t.Run("flush error", func(t *testing.T) {
		tempDir := t.TempDir()
		f, err := os.CreateTemp(tempDir, "flush-err-*")
		if err != nil {
			t.Fatalf("create temp: %v", err)
		}

		if closeErr := f.Close(); closeErr != nil {
			t.Fatalf("close file: %v", closeErr)
		}

		err = defaultExecuteAtomicWrite(context.Background(), f, f.Name(), filepath.Join(tempDir, "out_flush.pdf"), pages, nil)
		if err == nil || (!strings.Contains(err.Error(), "flush pdf buffer") && !strings.Contains(err.Error(), "counting writer write")) {
			t.Errorf("expected flush or write error on closed file, got: %v", err)
		}
	})
}

func TestExecuteAtomicWriteSyncCloseAndRenameErrors(t *testing.T) {
	origSync := fileSync
	origClose := fileClose
	origRename := osRename
	defer func() {
		fileSync = origSync
		fileClose = origClose
		osRename = origRename
	}()

	jpegBytes := createInternalTestJPEG(t)
	pages := []writer.Page{{Data: jpegBytes, Index: 1}}

	t.Run("file sync error", func(t *testing.T) {
		tempDir := t.TempDir()
		f, err := os.CreateTemp(tempDir, "sync-err-*")
		if err != nil {
			t.Fatalf("create temp: %v", err)
		}
		defer func() {
			if cerr := f.Close(); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
				t.Logf("cleanup file close: %v", cerr)
			}
		}()

		fileSync = mockFailingSync
		err = defaultExecuteAtomicWrite(context.Background(), f, f.Name(), filepath.Join(tempDir, "out_sync.pdf"), pages, nil)
		fileSync = origSync

		if err == nil || !strings.Contains(err.Error(), "sync pdf file") {
			t.Errorf("expected sync error, got: %v", err)
		}
	})

	t.Run("file close error", func(t *testing.T) {
		tempDir := t.TempDir()
		f, err := os.CreateTemp(tempDir, "close-err-*")
		if err != nil {
			t.Fatalf("create temp: %v", err)
		}

		fileClose = mockFailingClose
		err = defaultExecuteAtomicWrite(context.Background(), f, f.Name(), filepath.Join(tempDir, "out_close.pdf"), pages, nil)
		fileClose = origClose

		if err == nil || !strings.Contains(err.Error(), "close pdf file") {
			t.Errorf("expected close error, got: %v", err)
		}
	})

	t.Run("rename error", func(t *testing.T) {
		tempDir := t.TempDir()
		f, err := os.CreateTemp(tempDir, "rename-err-*")
		if err != nil {
			t.Fatalf("create temp: %v", err)
		}

		osRename = mockFailingRename
		err = defaultExecuteAtomicWrite(context.Background(), f, f.Name(), filepath.Join(tempDir, "out_rename.pdf"), pages, nil)
		osRename = origRename

		if err == nil || !strings.Contains(err.Error(), "atomic rename pdf") {
			t.Errorf("expected rename error, got: %v", err)
		}
	})

	t.Run("defer close error joined with write error", func(t *testing.T) {
		tempDir := t.TempDir()
		f, err := os.CreateTemp(tempDir, "defer-close-err-*")
		if err != nil {
			t.Fatalf("create temp: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		fileClose = mockFailingClose
		err = defaultExecuteAtomicWrite(ctx, f, f.Name(), filepath.Join(tempDir, "out_defer.pdf"), pages, nil)
		fileClose = origClose

		if err == nil || !strings.Contains(err.Error(), "close file in defer") {
			t.Errorf("expected joined defer close error, got: %v", err)
		}
	})
}

func TestWriteChapterJoinedRemoveError(t *testing.T) {
	origAtomicWrite := executeAtomicWrite
	origRemove := osRemove
	defer func() {
		executeAtomicWrite = origAtomicWrite
		osRemove = origRemove
	}()

	tempDir := t.TempDir()
	dest := filepath.Join(tempDir, "remove_err.pdf")
	pages := []writer.Page{{Data: []byte("dummy"), Index: 1}}

	executeAtomicWrite = mockFailingAtomicWrite
	osRemove = mockFailingRemove

	w := New()
	err := w.WriteChapter(context.Background(), dest, pages, nil)

	if err == nil || !strings.Contains(err.Error(), "remove tmp file") {
		t.Errorf("expected joined remove error, got: %v", err)
	}

	origMkdir := osMkdirAll
	osMkdirAll = mockFailingMkdirAll
	mkdirErr := w.WriteChapter(context.Background(), dest, pages, nil)
	osMkdirAll = origMkdir
	if mkdirErr == nil || !strings.Contains(mkdirErr.Error(), "create chapter directory") {
		t.Errorf("expected create chapter directory error, got: %v", mkdirErr)
	}
}
