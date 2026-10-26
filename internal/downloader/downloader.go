// Package downloader coordinates concurrent page retrieval and chapter compilation.
package downloader

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/underhax/go-webtoons-downloader/internal/webtoon"
	"github.com/underhax/go-webtoons-downloader/internal/writer"
)

// Downloader orchestrates page downloads and persists chapters using a ChapterWriter.
type Downloader struct {
	client      *webtoon.Client
	writer      writer.ChapterWriter
	concurrency int
	delay       time.Duration
}

// New constructs an instance of Downloader with configured concurrency and rate delay.
func New(client *webtoon.Client, w writer.ChapterWriter, concurrency int, delay time.Duration) *Downloader {
	if concurrency < 1 {
		concurrency = 1
	}

	return &Downloader{
		client:      client,
		writer:      w,
		concurrency: concurrency,
		delay:       delay,
	}
}

// ChapterHooks provides optional lifecycle callbacks for chapter download events.
type ChapterHooks struct {
	OnPagesFetched func(totalPages int) func()
	OnPageDone     func(completed, total int)
}

// DownloadChapter fetches all images for a chapter and saves them into the destination file.
func (d *Downloader) DownloadChapter(ctx context.Context, chapter webtoon.Chapter, destPath string, hooks *ChapterHooks, meta *writer.ChapterMetadata) error {
	imageURLs, err := d.client.FetchChapterImageURLs(ctx, chapter.ViewerURL)
	if err != nil {
		return fmt.Errorf("fetch chapter image urls: %w", err)
	}

	var stopHook func()
	if hooks != nil && hooks.OnPagesFetched != nil {
		stopHook = hooks.OnPagesFetched(len(imageURLs))
	}
	defer func() {
		if stopHook != nil {
			stopHook()
		}
	}()

	pages, err := d.downloadPages(ctx, imageURLs, hooks)
	if err != nil {
		return fmt.Errorf("download chapter pages: %w", err)
	}

	if err := d.writer.WriteChapter(ctx, destPath, pages, meta); err != nil {
		return fmt.Errorf("write chapter: %w", err)
	}

	return nil
}

func (d *Downloader) downloadPages(ctx context.Context, imageURLs []string, hooks *ChapterHooks) ([]writer.Page, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	total := len(imageURLs)
	pages := make([]writer.Page, total)
	errChan := make(chan error, total+1)
	sem := make(chan struct{}, d.concurrency)
	var wg sync.WaitGroup

	for i, imgURL := range imageURLs {
		if err := ctx.Err(); err != nil {
			errChan <- fmt.Errorf("context canceled before starting page %d: %w", i+1, err)
			break
		}

		wg.Add(1)
		go func(index int, url string) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				errChan <- fmt.Errorf("page %d waiting on semaphore: %w", index+1, ctx.Err())
				return
			}

			if d.delay > 0 {
				select {
				case <-time.After(d.delay):
				case <-ctx.Done():
					errChan <- fmt.Errorf("page %d delay interrupted: %w", index+1, ctx.Err())
					return
				}
			}

			data, err := d.client.DownloadImage(ctx, url)
			if err != nil {
				errChan <- fmt.Errorf("download page %d: %w", index+1, err)
				cancel()
				return
			}

			pages[index] = writer.Page{
				Data:  data,
				Index: index + 1,
			}

			if hooks != nil && hooks.OnPageDone != nil {
				hooks.OnPageDone(index+1, total)
			}
		}(i, imgURL)
	}

	wg.Wait()
	close(errChan)

	if firstErr := <-errChan; firstErr != nil {
		return nil, firstErr
	}

	return pages, nil
}
