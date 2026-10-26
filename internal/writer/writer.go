// Package writer defines abstractions and common contracts for saving downloaded comic pages.
package writer

import (
	"context"
	"errors"
)

// ErrEmptyPages indicates that no pages were provided to write.
var ErrEmptyPages = errors.New("cannot write empty chapter without pages")

// Page represents an individual comic page image to be written to storage.
type Page struct {
	Data  []byte
	Index int
}

// ChapterMetadata represents semantic metadata associated with a chapter for embedded document info.
type ChapterMetadata struct {
	SeriesTitle   string
	ChapterTitle  string
	Author        string
	Genre         string
	Summary       string
	WebURL        string
	ChapterNumber int
}

// ChapterWriter specifies the interface for persisting downloaded chapter pages.
type ChapterWriter interface {
	WriteChapter(ctx context.Context, destPath string, pages []Page, meta *ChapterMetadata) error
	Extension() string
}
