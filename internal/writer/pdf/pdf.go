package pdf

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/underhax/go-webtoons-downloader/internal/validator"
	"github.com/underhax/go-webtoons-downloader/internal/writer"
)

// Writer converts ordered chapter pages into a unified, lightweight PDF document.
type Writer struct{}

// New creates a new instance of the PDF chapter writer.
func New() *Writer {
	return &Writer{}
}

// Extension returns the file extension used for PDF chapters.
func (w *Writer) Extension() string {
	return ".pdf"
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

func defaultWritePDFDocument(ctx context.Context, bw *bufio.Writer, pages []writer.Page, meta *writer.ChapterMetadata) error {
	return writePDFDocument(ctx, bw, pages, meta)
}

var (
	osMkdirAll           = os.MkdirAll
	osOpenFile           = os.OpenFile
	osRemove             = os.Remove
	osRename             = os.Rename
	fileSync             = defaultFileSync
	fileClose            = defaultFileClose
	executeAtomicWrite   = defaultExecuteAtomicWrite
	writePDFDocumentFunc = defaultWritePDFDocument
)

// WriteChapter streams comic pages into a valid PDF 1.4 document using atomic file persistence.
func (w *Writer) WriteChapter(ctx context.Context, destPath string, pages []writer.Page, meta *writer.ChapterMetadata) error {
	if len(pages) == 0 {
		return writer.ErrEmptyPages
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context canceled before write: %w", err)
	}

	if _, err := validator.ValidateWriteTarget(destPath); err != nil {
		return fmt.Errorf("validate pdf destination: %w", err)
	}

	cleanDest := filepath.Clean(destPath)
	dir := filepath.Dir(cleanDest)
	if err := osMkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create chapter directory: %w", err)
	}

	tmpPath := filepath.Clean(cleanDest + ".tmp")
	file, err := osOpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open tmp chapter file: %w", err)
	}

	writeErr := executeAtomicWrite(ctx, file, tmpPath, cleanDest, pages, meta)
	if writeErr != nil {
		if rerr := osRemove(tmpPath); rerr != nil && !os.IsNotExist(rerr) {
			return errors.Join(writeErr, fmt.Errorf("remove tmp file: %w", rerr))
		}
		return writeErr
	}

	return nil
}

func defaultExecuteAtomicWrite(ctx context.Context, file *os.File, tmpPath, cleanDest string, pages []writer.Page, meta *writer.ChapterMetadata) (retErr error) {
	defer func() {
		if cerr := fileClose(file); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
			retErr = errors.Join(retErr, fmt.Errorf("close file in defer: %w", cerr))
		}
	}()

	bw := bufio.NewWriter(file)
	if err := writePDFDocumentFunc(ctx, bw, pages, meta); err != nil {
		return err
	}

	if err := bw.Flush(); err != nil {
		return fmt.Errorf("flush pdf buffer: %w", err)
	}

	if err := fileSync(file); err != nil {
		return fmt.Errorf("sync pdf file: %w", err)
	}

	if err := fileClose(file); err != nil {
		return fmt.Errorf("close pdf file: %w", err)
	}

	if err := osRename(tmpPath, cleanDest); err != nil {
		return fmt.Errorf("atomic rename pdf: %w", err)
	}

	return nil
}

type countingWriter struct {
	writer *bufio.Writer
	count  int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.writer.Write(p)
	c.count += int64(n)
	if err != nil {
		return n, fmt.Errorf("counting writer write: %w", err)
	}
	return n, nil
}

func writePDFDocument(ctx context.Context, bw *bufio.Writer, pages []writer.Page, meta *writer.ChapterMetadata) error {
	cw := &countingWriter{writer: bw}
	numPages := len(pages)
	pageObjs := 2 + (numPages * 3)
	infoObjID := pageObjs + 1
	totalObjs := infoObjID
	offsets := make([]int64, totalObjs+1)

	if err := writePDFHeaderAndCatalog(cw, offsets, numPages); err != nil {
		return err
	}

	for i, page := range pages {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context canceled during pdf generation: %w", err)
		}

		if err := writePageObjects(cw, page, i+1, offsets); err != nil {
			return err
		}
	}

	if err := writeInfoObject(cw, offsets, infoObjID, meta); err != nil {
		return err
	}

	return writeXrefAndTrailer(cw, offsets, totalObjs, infoObjID)
}

func writeInfoObject(cw *countingWriter, offsets []int64, infoObjID int, meta *writer.ChapterMetadata) error {
	offsets[infoObjID] = cw.count

	title, author, genre := resolvePDFMetadata(meta)

	infoStr := fmt.Sprintf(
		"%d 0 obj\n<< /Title (%s) /Author (%s) /Subject (%s) /Producer (webtoons-downloader) /CreationDate (D:%s) >>\nendobj\n",
		infoObjID,
		escapePDFString(title),
		escapePDFString(author),
		escapePDFString(genre),
		time.Now().UTC().Format("20060102150405Z"),
	)

	if _, err := cw.Write([]byte(infoStr)); err != nil {
		return fmt.Errorf("write info object: %w", err)
	}
	return nil
}

func resolvePDFMetadata(meta *writer.ChapterMetadata) (title, author, genre string) {
	if meta == nil {
		return "", "", ""
	}

	title = meta.ChapterTitle
	if title == "" && meta.ChapterNumber > 0 {
		title = fmt.Sprintf("Chapter %d", meta.ChapterNumber)
	}

	if meta.SeriesTitle != "" {
		if title != "" {
			title = fmt.Sprintf("%s - %s", meta.SeriesTitle, title)
		} else {
			title = meta.SeriesTitle
		}
	}

	return title, meta.Author, meta.Genre
}

func escapePDFString(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch r {
		case '(', ')', '\\':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func writePDFHeaderAndCatalog(cw *countingWriter, offsets []int64, numPages int) error {
	if _, err := cw.Write([]byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")); err != nil {
		return fmt.Errorf("write pdf header: %w", err)
	}

	offsets[1] = cw.count
	catalogStr := "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	if _, err := cw.Write([]byte(catalogStr)); err != nil {
		return fmt.Errorf("write catalog object: %w", err)
	}

	var kidsBuilder strings.Builder
	for i := 1; i <= numPages; i++ {
		pageObjID := 3 + ((i - 1) * 3)
		fmt.Fprintf(&kidsBuilder, "%d 0 R ", pageObjID)
	}

	offsets[2] = cw.count
	pagesRootStr := fmt.Sprintf("2 0 obj\n<< /Type /Pages /Kids [ %s] /Count %d >>\nendobj\n", kidsBuilder.String(), numPages)
	if _, err := cw.Write([]byte(pagesRootStr)); err != nil {
		return fmt.Errorf("write pages root object: %w", err)
	}

	return nil
}

func writePageObjects(cw *countingWriter, page writer.Page, pageIndex int, offsets []int64) error {
	meta, err := extractOrNormalizeImage(page.Data)
	if err != nil {
		return fmt.Errorf("process page %d image: %w", page.Index, err)
	}

	pageObjID := 3 + ((pageIndex - 1) * 3)
	contentObjID := pageObjID + 1
	imageObjID := pageObjID + 2

	offsets[pageObjID] = cw.count
	pageObjStr := fmt.Sprintf(
		"%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>\nendobj\n",
		pageObjID, meta.width, meta.height, imageObjID, contentObjID,
	)
	if _, err := cw.Write([]byte(pageObjStr)); err != nil {
		return fmt.Errorf("write page object %d: %w", pageIndex, err)
	}

	streamContent := fmt.Sprintf("q %d 0 0 %d 0 0 cm /Im0 Do Q\n", meta.width, meta.height)
	offsets[contentObjID] = cw.count
	contentObjStr := fmt.Sprintf(
		"%d 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n",
		contentObjID, len(streamContent), streamContent,
	)
	if _, err := cw.Write([]byte(contentObjStr)); err != nil {
		return fmt.Errorf("write content object %d: %w", pageIndex, err)
	}

	offsets[imageObjID] = cw.count
	imageHeader := fmt.Sprintf(
		"%d 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n",
		imageObjID, meta.width, meta.height, len(meta.data),
	)
	if _, err := cw.Write([]byte(imageHeader)); err != nil {
		return fmt.Errorf("write image header %d: %w", pageIndex, err)
	}

	if _, err := cw.Write(meta.data); err != nil {
		return fmt.Errorf("write image stream data %d: %w", pageIndex, err)
	}

	if _, err := cw.Write([]byte("\nendstream\nendobj\n")); err != nil {
		return fmt.Errorf("write image footer %d: %w", pageIndex, err)
	}

	return nil
}

func writeXrefAndTrailer(cw *countingWriter, offsets []int64, totalObjs, infoObjID int) error {
	xrefOffset := cw.count
	xrefHeader := fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", totalObjs+1)
	if _, err := cw.Write([]byte(xrefHeader)); err != nil {
		return fmt.Errorf("write xref header: %w", err)
	}

	for objID := 1; objID <= totalObjs; objID++ {
		entry := fmt.Sprintf("%010d 00000 n \n", offsets[objID])
		if _, err := cw.Write([]byte(entry)); err != nil {
			return fmt.Errorf("write xref entry %d: %w", objID, err)
		}
	}

	trailerStr := fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", totalObjs+1, infoObjID, xrefOffset)
	if _, err := cw.Write([]byte(trailerStr)); err != nil {
		return fmt.Errorf("write trailer: %w", err)
	}

	return nil
}
