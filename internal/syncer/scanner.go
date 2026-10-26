package syncer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	reDigits  = regexp.MustCompile(`(\d+)`)
	osReadDir = os.ReadDir
)

const maxPathSegmentBytes = 240

// DetectLastChapter scans the folder for existing chapter files and extracts the highest chapter number.
func DetectLastChapter(folderPath, formatExt string) (int, error) {
	cleanPath := filepath.Clean(folderPath)
	entries, err := osReadDir(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read chapter directory %s: %w", cleanPath, err)
	}

	maxChapter := 0
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if !matchesChapterEntry(entry, formatExt) {
			continue
		}

		matches := reDigits.FindStringSubmatch(entry.Name())
		if len(matches) >= 2 {
			chapterNum, parseErr := strconv.Atoi(matches[1])
			if parseErr == nil && chapterNum > maxChapter {
				maxChapter = chapterNum
			}
		}
	}

	return maxChapter, nil
}

func matchesChapterEntry(entry os.DirEntry, formatExt string) bool {
	if formatExt == "" {
		return entry.IsDir()
	}
	if entry.IsDir() {
		return false
	}
	return strings.HasSuffix(strings.ToLower(entry.Name()), strings.ToLower(formatExt))
}

// BuildFolderName creates a deterministic folder name with 3-digit padded indexing "%03d. %s".
func BuildFolderName(index int, title string) string {
	cleanTitle := sanitizePathSegment(title)
	if index > 0 {
		return fmt.Sprintf("%03d. %s", index, cleanTitle)
	}
	return cleanTitle
}

func sanitizePathSegment(segment string) string {
	var sb strings.Builder
	sb.Grow(len(segment))

	for _, r := range segment {
		if r < 32 || r == 127 {
			continue
		}
		replaceDisallowedRune(&sb, r)
	}

	collapsed := collapseWords(sb.String())
	trimmed := strings.Trim(collapsed, " -.")
	truncated := truncateUTF8(trimmed, maxPathSegmentBytes)

	if truncated == "" || truncated == "." || truncated == ".." {
		return "webtoon"
	}

	if isWindowsReservedName(truncated) {
		return truncated + "_"
	}

	return truncated
}

func replaceDisallowedRune(sb *strings.Builder, r rune) {
	switch r {
	case '/', '\\', ':':
		sb.WriteString(" - ")
	case '?', '*':
		return
	case '"':
		sb.WriteByte('\'')
	case '<', '>', '|':
		sb.WriteByte('-')
	default:
		sb.WriteRune(r)
	}
}

func collapseWords(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}

	result := make([]string, 0, len(fields))
	lastWasHyphen := false

	for _, f := range fields {
		if f == "-" {
			if lastWasHyphen || len(result) == 0 {
				continue
			}
			lastWasHyphen = true
		} else {
			lastWasHyphen = false
		}
		result = append(result, f)
	}

	return strings.Join(result, " ")
}

func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}

	for i := maxBytes; i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return strings.TrimRight(s[:i], " -.")
		}
	}
	return s[:maxBytes]
}

func isWindowsReservedName(name string) bool {
	upper := strings.ToUpper(name)
	switch upper {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}
