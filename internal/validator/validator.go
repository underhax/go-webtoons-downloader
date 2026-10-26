// Package validator enforces security boundaries and domain constraints on configuration parameters and filesystem paths.
package validator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/underhax/go-webtoons-downloader/internal/webtoon"
)

// Validation error sentinels returned when user input or filesystem state violates security or domain rules.
var (
	ErrInvalidFormat      = errors.New("invalid output format")
	ErrInvalidNaming      = errors.New("invalid naming style (must be 'slug' or 'title')")
	ErrInvalidConcurrency = errors.New("concurrency must be at least 1")
	ErrInvalidDelay       = errors.New("delay cannot be negative")
	ErrInvalidTimeout     = errors.New("timeout must be greater than 0")
	ErrInvalidIndex       = errors.New("index must be at least 1")
	ErrInvalidStart       = errors.New("start chapter must be at least 1")
	ErrConflictingOptions = errors.New("conflicting options provided")
	ErrInvalidURL         = errors.New("invalid webtoon series url")
	ErrInvalidPath        = errors.New("invalid file system path")
	ErrSymlinkForbidden   = errors.New("symlinks are strictly prohibited")
	ErrPathTraversal      = errors.New("path traversal outside base directory is prohibited")
	ErrInvalidEnvVar      = errors.New("invalid environment variable value")
)

// Supported output formats and folder naming styles.
const (
	FormatPDF    = "pdf"
	FormatCBZ    = "cbz"
	FormatZIP    = "zip"
	FormatImages = "images"

	NamingSlug  = "slug"
	NamingTitle = "title"
)

// Filesystem resolution hooks replaceable for OS-independent unit testing.
var (
	LstatFunc = os.Lstat
	AbsFunc   = filepath.Abs
)

// ValidateFormat ensures the requested export format is supported by the writer registry.
func ValidateFormat(format string) error {
	switch format {
	case FormatPDF, FormatCBZ, FormatZIP, FormatImages:
		return nil
	default:
		return fmt.Errorf("%w: %s (supported: pdf, cbz, zip, images)", ErrInvalidFormat, format)
	}
}

// ValidateNaming ensures the directory naming strategy matches a supported mode.
func ValidateNaming(naming string) error {
	switch naming {
	case NamingSlug, NamingTitle:
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrInvalidNaming, naming)
	}
}

// ValidateConcurrency prevents worker pool starvation or invalid negative limits.
func ValidateConcurrency(concurrency int) error {
	if concurrency < 1 {
		return ErrInvalidConcurrency
	}
	return nil
}

// ValidateDelay prevents negative rate-limiting intervals.
func ValidateDelay(delay time.Duration) error {
	if delay < 0 {
		return ErrInvalidDelay
	}
	return nil
}

// ValidateTimeout ensures network requests have a positive deadline.
func ValidateTimeout(timeout time.Duration) error {
	if timeout <= 0 {
		return ErrInvalidTimeout
	}
	return nil
}

// ValidateIndex ensures an explicitly provided series index is 1-based.
func ValidateIndex(index int) error {
	if index < 1 {
		return ErrInvalidIndex
	}
	return nil
}

// ValidateStart ensures an explicitly provided starting chapter number is 1-based.
func ValidateStart(start int) error {
	if start < 1 {
		return ErrInvalidStart
	}
	return nil
}

// ValidateSeriesURL verifies that a URL conforms to the canonical Webtoon series format.
func ValidateSeriesURL(rawURL string) error {
	if !webtoon.IsValidSeriesURL(rawURL) {
		return fmt.Errorf("%w: %s", ErrInvalidURL, rawURL)
	}
	return nil
}

// ValidateOptionConflicts prevents mutually exclusive or incomplete CLI and environment combinations.
func ValidateOptionConflicts(url string, index, start int, explicitLinks bool) error {
	if url != "" && index > 0 {
		return fmt.Errorf("%w: --index cannot be used with --url", ErrConflictingOptions)
	}
	if url != "" && explicitLinks {
		return fmt.Errorf("%w: --links cannot be used with --url", ErrConflictingOptions)
	}
	if start > 0 && index == 0 && url == "" {
		return fmt.Errorf("%w: start chapter requires specific index or single url", ErrConflictingOptions)
	}
	return nil
}

// ValidateDirPath verifies that a directory path is free of traversal, control characters, root targets, and symlinks.
func ValidateDirPath(rawPath string) (string, error) {
	if err := validateRawPathSyntax(rawPath); err != nil {
		return "", err
	}

	cleanPath := filepath.Clean(rawPath)
	if cleanPath == "." {
		return "", fmt.Errorf("%w: cannot use current directory '.' as target", ErrInvalidPath)
	}

	absPath, err := AbsFunc(cleanPath)
	if err != nil {
		return "", fmt.Errorf("%w: resolve absolute path: %w", ErrInvalidPath, err)
	}

	if filepath.Dir(absPath) == absPath {
		return "", fmt.Errorf("%w: cannot use filesystem root directory %s", ErrInvalidPath, absPath)
	}

	chain := splitPathChain(absPath)
	for i, comp := range chain {
		isTarget := i == len(chain)-1
		if err := inspectComponent(comp, isTarget, true, false); err != nil {
			return "", err
		}
	}

	return cleanPath, nil
}

// ValidateLinksFilePath verifies that a links file path is free of traversal, control characters, directories, and symlinks.
func ValidateLinksFilePath(rawPath string) (string, error) {
	if err := validateRawPathSyntax(rawPath); err != nil {
		return "", err
	}

	cleanPath := filepath.Clean(rawPath)
	if cleanPath == "." {
		return "", fmt.Errorf("%w: invalid links file path '.'", ErrInvalidPath)
	}

	absPath, err := AbsFunc(cleanPath)
	if err != nil {
		return "", fmt.Errorf("%w: resolve absolute path: %w", ErrInvalidPath, err)
	}

	if filepath.Dir(absPath) == absPath {
		return "", fmt.Errorf("%w: links path cannot be root directory %s", ErrInvalidPath, absPath)
	}

	chain := splitPathChain(absPath)
	for i, comp := range chain {
		isTarget := i == len(chain)-1
		if err := inspectComponent(comp, isTarget, false, true); err != nil {
			return "", err
		}
	}

	return cleanPath, nil
}

// ValidateSubPath enforces jail containment of targetPath strictly within baseDir and rejects symlinks.
func ValidateSubPath(baseDir, targetPath string) (string, error) {
	if err := validateRawPathSyntax(baseDir); err != nil {
		return "", err
	}
	if err := validateRawPathSyntax(targetPath); err != nil {
		return "", err
	}

	cleanBase := filepath.Clean(baseDir)
	cleanTarget := filepath.Clean(targetPath)

	absBase, err := AbsFunc(cleanBase)
	if err != nil {
		return "", fmt.Errorf("%w: resolve base path: %w", ErrInvalidPath, err)
	}

	absTarget, err := AbsFunc(cleanTarget)
	if err != nil {
		return "", fmt.Errorf("%w: resolve target path: %w", ErrInvalidPath, err)
	}

	rel, relErr := filepath.Rel(absBase, absTarget)
	if relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s escapes base directory %s", ErrPathTraversal, targetPath, baseDir)
	}

	curr := absBase
	for seg := range strings.SplitSeq(rel, string(filepath.Separator)) {
		curr = filepath.Join(curr, seg)
		if err := inspectNoSymlink(curr); err != nil {
			return "", err
		}
	}

	return cleanTarget, nil
}

// ValidateWriteTarget ensures a destination file path and its temporary staging path are not symlinks.
func ValidateWriteTarget(destPath string) (string, error) {
	if err := validateRawPathSyntax(destPath); err != nil {
		return "", err
	}

	cleanDest := filepath.Clean(destPath)
	if cleanDest == "." || filepath.Dir(cleanDest) == cleanDest {
		return "", fmt.Errorf("%w: invalid write destination %s", ErrInvalidPath, destPath)
	}

	if err := inspectNoSymlink(filepath.Dir(cleanDest)); err != nil {
		return "", err
	}
	if err := inspectNoSymlink(cleanDest); err != nil {
		return "", err
	}
	if err := inspectNoSymlink(cleanDest + ".tmp"); err != nil {
		return "", err
	}

	return cleanDest, nil
}

func validateRawPathSyntax(rawPath string) error {
	if strings.TrimSpace(rawPath) == "" {
		return fmt.Errorf("%w: path cannot be empty", ErrInvalidPath)
	}
	for _, r := range rawPath {
		if r < 32 || r == 127 {
			return fmt.Errorf("%w: path contains control characters", ErrInvalidPath)
		}
	}
	if hasDotDotSegment(rawPath) {
		return fmt.Errorf("%w: parent traversal '..' is forbidden in %s", ErrPathTraversal, rawPath)
	}
	return nil
}

func hasDotDotSegment(rawPath string) bool {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	for seg := range strings.SplitSeq(normalized, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

func splitPathChain(absPath string) []string {
	vol := filepath.VolumeName(absPath)
	trimmed := strings.TrimLeft(absPath[len(vol):], `/\`)

	parts := strings.Split(trimmed, string(filepath.Separator))
	chain := make([]string, 0, len(parts))
	curr := vol + string(filepath.Separator)

	for _, part := range parts {
		curr = filepath.Join(curr, part)
		chain = append(chain, curr)
	}

	return chain
}

func inspectComponent(path string, isTarget, mustBeDir, mustBeRegular bool) error {
	fi, err := LstatFunc(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("%w: stat %s: %w", ErrInvalidPath, path, err)
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: symlink detected at %s", ErrSymlinkForbidden, path)
	}

	if !isTarget {
		if !fi.IsDir() {
			return fmt.Errorf("%w: parent %s is not a directory", ErrInvalidPath, path)
		}
		return nil
	}

	if mustBeDir && !fi.IsDir() {
		return fmt.Errorf("%w: target %s is not a directory", ErrInvalidPath, path)
	}

	if mustBeRegular && !fi.Mode().IsRegular() {
		return fmt.Errorf("%w: target %s is not a regular file", ErrInvalidPath, path)
	}

	return nil
}

func inspectNoSymlink(path string) error {
	fi, err := LstatFunc(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("%w: stat %s: %w", ErrInvalidPath, path, err)
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: symlink detected at %s", ErrSymlinkForbidden, path)
	}

	if !fi.Mode().IsRegular() && !fi.IsDir() {
		return fmt.Errorf("%w: irregular file detected at %s", ErrInvalidPath, path)
	}

	return nil
}
