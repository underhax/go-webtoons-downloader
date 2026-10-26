// Package syncer manages links parsing, local chapter scanning, and delta synchronization.
package syncer

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/underhax/go-webtoons-downloader/internal/validator"
	"github.com/underhax/go-webtoons-downloader/internal/webtoon"
)

// ErrEmptyLinksFile indicates that no valid URLs were found in the links source.
var ErrEmptyLinksFile = errors.New("no valid webtoon links found")

// LinkEntry describes an individual comic series line item in links.txt.
type LinkEntry struct {
	URL   string
	Index int
}

// ParseLinks reads sequential links from an io.Reader, skipping empty lines, comments, and invalid URLs.
func ParseLinks(r io.Reader) ([]LinkEntry, error) {
	scanner := bufio.NewScanner(r)
	entries := make([]LinkEntry, 0, 16)
	currentIndex := 1

	for scanner.Scan() {
		rawLine := scanner.Text()

		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		cleanLine := stripInlineComment(trimmed)
		cleanLine = strings.Trim(cleanLine, "\"'")
		if cleanLine == "" {
			continue
		}

		if !webtoon.IsValidSeriesURL(cleanLine) {
			continue
		}

		entries = append(entries, LinkEntry{
			URL:   cleanLine,
			Index: currentIndex,
		})
		currentIndex++
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read links content: %w", err)
	}

	if len(entries) == 0 {
		return nil, ErrEmptyLinksFile
	}

	return entries, nil
}

func defaultFileClose(f *os.File) error {
	if err := f.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}
	return nil
}

var fileClose = defaultFileClose

// ParseLinksFile opens the specified file and parses its links.
func ParseLinksFile(filePath string) ([]LinkEntry, error) {
	if _, err := validator.ValidateLinksFilePath(filePath); err != nil {
		return nil, fmt.Errorf("validate links file path: %w", err)
	}

	cleanPath := filepath.Clean(filePath)
	file, err := os.Open(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("open links file: %w", err)
	}
	defer func() {
		if cerr := fileClose(file); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
			return
		}
	}()

	return ParseLinks(file)
}

func stripInlineComment(line string) string {
	inQuote := false
	var quoteChar rune

	for i, r := range line {
		if (r == '"' || r == '\'') && !inQuote {
			inQuote = true
			quoteChar = r
			continue
		}
		if inQuote && r == quoteChar {
			inQuote = false
			continue
		}
		if r == '#' && !inQuote {
			return strings.TrimSpace(line[:i])
		}
	}

	return strings.TrimSpace(line)
}
