package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/underhax/go-webtoons-downloader/internal/config"
	"github.com/underhax/go-webtoons-downloader/internal/downloader"
	"github.com/underhax/go-webtoons-downloader/internal/ui"
	"github.com/underhax/go-webtoons-downloader/internal/validator"
	"github.com/underhax/go-webtoons-downloader/internal/webtoon"
	"github.com/underhax/go-webtoons-downloader/internal/writer"
	"github.com/underhax/go-webtoons-downloader/internal/writer/pdf"
	"github.com/underhax/go-webtoons-downloader/internal/writer/raw"
	"github.com/underhax/go-webtoons-downloader/internal/writer/zip"
)

var (
	// ErrSyncFailure indicates that one or more titles failed during the sync process.
	ErrSyncFailure = errors.New("sync encountered errors on one or more titles")

	// ErrTargetIndexNotFound indicates that the requested --index was not found in links.txt.
	ErrTargetIndexNotFound = errors.New("target index not found in links")
)

// Syncer coordinates multi-series downloads and directory synchronization.
type Syncer struct {
	cfg        *config.Config
	client     *webtoon.Client
	writer     writer.ChapterWriter
	downloader *downloader.Downloader
	out        io.Writer
}

// New constructs a Syncer instance from configuration.
func New(cfg *config.Config, client *webtoon.Client, out io.Writer) (*Syncer, error) {
	w, err := resolveWriter(cfg.Format)
	if err != nil {
		return nil, err
	}

	dl := downloader.New(client, w, cfg.Concurrency, cfg.Delay)

	return &Syncer{
		cfg:        cfg,
		client:     client,
		writer:     w,
		downloader: dl,
		out:        out,
	}, nil
}

func resolveWriter(format string) (writer.ChapterWriter, error) {
	switch format {
	case config.FormatPDF:
		return pdf.New(), nil
	case config.FormatCBZ:
		return zip.NewCBZ(), nil
	case config.FormatZIP:
		return zip.NewZIP(), nil
	case config.FormatImages:
		return raw.New(), nil
	default:
		return nil, fmt.Errorf("%w: %s", config.ErrInvalidFormat, format)
	}
}

// Run executes the synchronization process for either single URL, specific index, or all links.
func (s *Syncer) Run(ctx context.Context) error {
	cleanDir, err := validator.ValidateDirPath(s.cfg.Directory)
	if err != nil {
		return fmt.Errorf("validate base directory: %w", err)
	}

	if mkErr := os.MkdirAll(cleanDir, 0o700); mkErr != nil {
		return fmt.Errorf("create base directory %s: %w", cleanDir, mkErr)
	}

	if s.cfg.URL != "" {
		return s.syncSingleURL(ctx, s.cfg.URL, 0, s.cfg.Start)
	}

	entries, err := ParseLinksFile(s.cfg.LinksFile)
	if err != nil {
		return fmt.Errorf("load links: %w", err)
	}

	if s.cfg.Index > 0 {
		targetEntry, found := findEntryByIndex(entries, s.cfg.Index)
		if !found {
			return fmt.Errorf("%w: %d", ErrTargetIndexNotFound, s.cfg.Index)
		}
		return s.syncSingleURL(ctx, targetEntry.URL, targetEntry.Index, s.cfg.Start)
	}

	return s.syncAllEntries(ctx, entries)
}

func findEntryByIndex(entries []LinkEntry, targetIdx int) (LinkEntry, bool) {
	for _, entry := range entries {
		if entry.Index == targetIdx {
			return entry, true
		}
	}
	return LinkEntry{}, false
}

func (s *Syncer) syncAllEntries(ctx context.Context, entries []LinkEntry) error {
	var failureCount int

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("sync aborted: %w", err)
		}

		if err := s.syncSingleURL(ctx, entry.URL, entry.Index, 0); err != nil {
			failureCount++
			if _, werr := fmt.Fprintf(s.out, "[ERROR] Title %d (%s) failed: %v\n", entry.Index, entry.URL, err); werr != nil {
				return fmt.Errorf("write error log: %w", werr)
			}
		}
	}

	if failureCount > 0 {
		return fmt.Errorf("%w: %d titles failed", ErrSyncFailure, failureCount)
	}

	return nil
}

func (s *Syncer) syncSingleURL(ctx context.Context, seriesURL string, index, manualStart int) error {
	meta, err := s.client.FetchSeriesMetadata(ctx, seriesURL)
	if err != nil {
		return fmt.Errorf("fetch series metadata: %w", err)
	}

	folderBase := meta.TitleSlug
	if s.cfg.Naming == config.NamingTitle {
		folderBase = meta.TitleName
	}

	folderName := BuildFolderName(index, folderBase)
	folderPath, err := validator.ValidateSubPath(s.cfg.Directory, filepath.Join(s.cfg.Directory, folderName))
	if err != nil {
		return fmt.Errorf("validate series folder path: %w", err)
	}
	if mkErr := os.MkdirAll(folderPath, 0o700); mkErr != nil {
		return fmt.Errorf("create series folder %s: %w", folderPath, mkErr)
	}

	if s.shouldWriteSeriesJSON() {
		if werr := writeSeriesJSON(folderPath, meta, seriesURL); werr != nil {
			return fmt.Errorf("write series metadata json: %w", werr)
		}
	}

	chapters, err := s.client.FetchChapters(ctx, meta)
	if err != nil {
		return fmt.Errorf("fetch chapters for %s: %w", meta.TitleName, err)
	}

	startChapter := manualStart
	var lastExisting int
	if startChapter <= 0 {
		var err error
		lastExisting, err = detectLastChapterFunc(folderPath, s.writer.Extension())
		if err != nil {
			return fmt.Errorf("detect last chapter: %w", err)
		}
		startChapter = lastExisting + 1
	}

	if startChapter > len(chapters) {
		return s.logAlreadyUpToDate(meta.TitleName, manualStart, lastExisting, len(chapters))
	}

	return s.downloadChapterDelta(ctx, chapters, startChapter, folderPath, meta)
}

func (s *Syncer) shouldWriteSeriesJSON() bool {
	return s.cfg.SeriesJSON || s.cfg.Format == config.FormatImages
}

func (s *Syncer) logAlreadyUpToDate(title string, manualStart, lastExisting, total int) error {
	if s.cfg.Silent {
		return nil
	}

	if manualStart > 0 {
		if _, err := fmt.Fprintf(s.out, "[UP-TO-DATE] %s: start chapter %d exceeds available chapters (%d)\n", title, manualStart, total); err != nil {
			return fmt.Errorf("write log: %w", err)
		}
		return nil
	}

	if lastExisting > 0 {
		if _, err := fmt.Fprintf(s.out, "[UP-TO-DATE] %s: up to date at chapter %d\n", title, lastExisting); err != nil {
			return fmt.Errorf("write log: %w", err)
		}
		return nil
	}

	if _, err := fmt.Fprintf(s.out, "[UP-TO-DATE] %s: no chapters available\n", title); err != nil {
		return fmt.Errorf("write log: %w", err)
	}

	return nil
}

type deltaResult struct {
	skippedChapters []int
	downloadedCount int
	startChapter    int
	totalChapters   int
}

func (r *deltaResult) recordChapter(chNum int, downloaded bool) {
	if downloaded {
		r.downloadedCount++
		return
	}

	r.skippedChapters = append(r.skippedChapters, chNum)
}

func formatChapterList(chapters []int) string {
	if len(chapters) == 0 {
		return ""
	}

	var parts []string
	n := len(chapters)
	i := 0

	for i < n {
		start := chapters[i]
		j := i
		for j+1 < n && chapters[j+1] == chapters[j]+1 {
			j++
		}

		span := j - i + 1
		switch {
		case span >= 3:
			parts = append(parts, fmt.Sprintf("%d-%d", start, chapters[j]))
		case span == 2:
			parts = append(parts, strconv.Itoa(start), strconv.Itoa(chapters[j]))
		default:
			parts = append(parts, strconv.Itoa(start))
		}

		i = j + 1
	}

	return strings.Join(parts, ", ")
}

func (s *Syncer) downloadChapterDelta(ctx context.Context, chapters []webtoon.Chapter, startChapter int, folderPath string, meta *webtoon.SeriesInfo) error {
	total := len(chapters)
	digits := max(3, len(strconv.Itoa(total)))
	formatPattern := fmt.Sprintf("%%0%dd%s", digits, s.writer.Extension())

	res := deltaResult{
		startChapter:  startChapter,
		totalChapters: total,
	}

	var sp *ui.Spinner
	if !s.cfg.Silent {
		defer func() {
			sp.Stop()
		}()
	}

	for i := startChapter - 1; i < total; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("delta download canceled: %w", err)
		}

		ch := chapters[i]
		downloaded, err := s.downloadChapterIfMissing(ctx, ch, folderPath, formatPattern, total, meta, &sp)
		if err != nil {
			return err
		}

		res.recordChapter(ch.Number, downloaded)
	}

	sp.Stop()
	return s.logCompletion(meta.TitleName, res)
}

func (s *Syncer) downloadChapterIfMissing(ctx context.Context, ch webtoon.Chapter, folderPath, formatPattern string, total int, meta *webtoon.SeriesInfo, spPtr **ui.Spinner) (bool, error) {
	if findExistingChapterFile(folderPath, s.writer.Extension(), ch.Number, formatPattern) != "" {
		return false, nil
	}

	fileName := fmt.Sprintf(formatPattern, ch.Number)
	destPath, err := validator.ValidateSubPath(folderPath, filepath.Join(folderPath, fileName))
	if err != nil {
		return false, fmt.Errorf("validate chapter path: %w", err)
	}

	label := fmt.Sprintf("[%s] Chapter %d/%d", meta.TitleName, ch.Number, total)
	hooks := s.buildChapterHooks(ctx, label, spPtr)

	chapterMeta := &writer.ChapterMetadata{
		SeriesTitle:   meta.TitleName,
		ChapterTitle:  ch.Title,
		Author:        meta.Author,
		Genre:         meta.Genre,
		Summary:       meta.Summary,
		WebURL:        ch.ViewerURL,
		ChapterNumber: ch.Number,
	}

	if err := s.downloader.DownloadChapter(ctx, ch, destPath, hooks, chapterMeta); err != nil {
		return false, fmt.Errorf("download chapter %d: %w", ch.Number, err)
	}

	return true, nil
}

func (s *Syncer) buildChapterHooks(ctx context.Context, label string, spPtr **ui.Spinner) *downloader.ChapterHooks {
	if s.cfg.Silent {
		return nil
	}

	if spPtr != nil {
		if *spPtr == nil {
			*spPtr = ui.Start(ctx, label, nil, 0, s.out)
		} else {
			(*spPtr).Update(label, 0, nil)
		}
	}

	completed := &atomic.Int32{}
	return &downloader.ChapterHooks{
		OnPagesFetched: func(totalPages int) func() {
			if spPtr != nil && *spPtr != nil {
				(*spPtr).Update(label, totalPages, completed)
			}
			return nil
		},
		OnPageDone: func(_, _ int) {
			completed.Add(1)
		},
	}
}

func (s *Syncer) logCompletion(title string, res deltaResult) error {
	if s.cfg.Silent {
		return nil
	}

	if res.downloadedCount == 0 {
		return s.logNoDownloads(title, res)
	}

	return s.logWithDownloads(title, res)
}

func (s *Syncer) logNoDownloads(title string, res deltaResult) error {
	skipCount := len(res.skippedChapters)
	if skipCount == 1 {
		if _, err := fmt.Fprintf(s.out, "[UP-TO-DATE] %s: chapter %d already present\n", title, res.skippedChapters[0]); err != nil {
			return fmt.Errorf("write log: %w", err)
		}
		return nil
	}

	if skipCount > 1 {
		formattedList := formatChapterList(res.skippedChapters)
		if _, err := fmt.Fprintf(s.out, "[UP-TO-DATE] %s: chapters %s already present\n", title, formattedList); err != nil {
			return fmt.Errorf("write log: %w", err)
		}
		return nil
	}

	if _, err := fmt.Fprintf(s.out, "[UP-TO-DATE] %s: up to date at chapter %d\n", title, res.totalChapters); err != nil {
		return fmt.Errorf("write log: %w", err)
	}
	return nil
}

func (s *Syncer) logWithDownloads(title string, res deltaResult) error {
	if len(res.skippedChapters) == 0 {
		return s.logDownloadsWithoutSkips(title, res)
	}
	return s.logDownloadsWithSkips(title, res)
}

func (s *Syncer) logDownloadsWithoutSkips(title string, res deltaResult) error {
	if res.startChapter == 1 && res.downloadedCount == res.totalChapters {
		if _, err := fmt.Fprintf(s.out, "[SERIES COMPLETED] %s: all %d chapters downloaded\n", title, res.totalChapters); err != nil {
			return fmt.Errorf("write log: %w", err)
		}
		return nil
	}

	if res.downloadedCount == 1 {
		if _, err := fmt.Fprintf(s.out, "[SERIES COMPLETED] %s: 1 chapter downloaded\n", title); err != nil {
			return fmt.Errorf("write log: %w", err)
		}
		return nil
	}

	if _, err := fmt.Fprintf(s.out, "[SERIES COMPLETED] %s: %d chapters downloaded\n", title, res.downloadedCount); err != nil {
		return fmt.Errorf("write log: %w", err)
	}
	return nil
}

func (s *Syncer) logDownloadsWithSkips(title string, res deltaResult) error {
	rangeTotal := res.totalChapters - res.startChapter + 1
	skipDetail := "skipped chapters: " + formatChapterList(res.skippedChapters)

	if res.downloadedCount == 1 {
		if _, err := fmt.Fprintf(s.out, "[SERIES COMPLETED] %s: 1 of %d chapters downloaded (%s)\n", title, rangeTotal, skipDetail); err != nil {
			return fmt.Errorf("write log: %w", err)
		}
		return nil
	}

	if _, err := fmt.Fprintf(s.out, "[SERIES COMPLETED] %s: %d of %d chapters downloaded (%s)\n", title, res.downloadedCount, rangeTotal, skipDetail); err != nil {
		return fmt.Errorf("write log: %w", err)
	}
	return nil
}

type komgaSeriesJSON struct {
	Title     string   `json:"title"`
	Summary   string   `json:"summary,omitempty"`
	Status    string   `json:"status,omitempty"`
	Publisher string   `json:"publisher,omitempty"`
	URL       string   `json:"url,omitempty"`
	Genres    []string `json:"genres,omitempty"`
	Authors   []string `json:"authors,omitempty"`
}

func defaultJSONMarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}

func defaultOSWriteFile(name string, data []byte, perm os.FileMode) error {
	if err := os.WriteFile(name, data, perm); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

func defaultDetectLastChapter(folderPath, formatExt string) (int, error) {
	return DetectLastChapter(folderPath, formatExt)
}

var (
	jsonMarshalIndent     = defaultJSONMarshalIndent
	osWriteFile           = defaultOSWriteFile
	osRename              = os.Rename
	detectLastChapterFunc = defaultDetectLastChapter
)

func writeSeriesJSON(folderPath string, meta *webtoon.SeriesInfo, rawURL string) error {
	var genres []string
	if meta.Genre != "" {
		genres = []string{meta.Genre}
	}

	var authors []string
	if meta.Author != "" {
		for a := range strings.SplitSeq(meta.Author, ",") {
			if trimmed := strings.TrimSpace(a); trimmed != "" {
				authors = append(authors, trimmed)
			}
		}
	}

	doc := komgaSeriesJSON{
		Title:     meta.TitleName,
		Summary:   meta.Summary,
		Status:    "ONGOING",
		Publisher: "WEBTOON",
		Genres:    genres,
		Authors:   authors,
		URL:       rawURL,
	}

	data, err := jsonMarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal series json: %w", err)
	}

	targetPath, err := validator.ValidateWriteTarget(filepath.Join(folderPath, "series.json"))
	if err != nil {
		return fmt.Errorf("validate series json path: %w", err)
	}
	tmpPath := targetPath + ".tmp"

	if err := osWriteFile(tmpPath, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write tmp series json: %w", err)
	}

	if err := osRename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("atomic rename series json: %w", err)
	}

	return nil
}

func findExistingChapterFile(folderPath, ext string, number int, formatPattern string) string {
	primaryName := fmt.Sprintf(formatPattern, number)
	primaryPath := filepath.Join(folderPath, primaryName)
	if fi, err := os.Lstat(primaryPath); err == nil && fi.Mode()&os.ModeSymlink == 0 && (fi.IsDir() || fi.Size() > 0) {
		return primaryName
	}

	legacyName := fmt.Sprintf("%d%s", number, ext)
	legacyPath := filepath.Join(folderPath, legacyName)
	if fi, err := os.Lstat(legacyPath); err == nil && fi.Mode()&os.ModeSymlink == 0 && (fi.IsDir() || fi.Size() > 0) {
		return legacyName
	}

	return ""
}
