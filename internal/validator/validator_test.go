package validator

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type mockFileInfo struct {
	name  string
	mode  os.FileMode
	isDir bool
}

func (m mockFileInfo) Name() string       { return m.name }
func (m mockFileInfo) Size() int64        { return 0 }
func (m mockFileInfo) Mode() os.FileMode  { return m.mode }
func (m mockFileInfo) ModTime() time.Time { return time.Time{} }
func (m mockFileInfo) IsDir() bool        { return m.isDir }
func (m mockFileInfo) Sys() any           { return nil }

func TestFormatAndNamingValidators(t *testing.T) {
	formatCases := []struct {
		expectErr error
		name      string
		val       string
	}{
		{name: "pdf", val: FormatPDF, expectErr: nil},
		{name: "cbz", val: FormatCBZ, expectErr: nil},
		{name: "zip", val: FormatZIP, expectErr: nil},
		{name: "images", val: FormatImages, expectErr: nil},
		{name: "epub invalid", val: "epub", expectErr: ErrInvalidFormat},
		{name: "empty invalid", val: "", expectErr: ErrInvalidFormat},
	}

	for _, tc := range formatCases {
		t.Run("Format_"+tc.name, func(t *testing.T) {
			err := ValidateFormat(tc.val)
			if !errors.Is(err, tc.expectErr) {
				t.Fatalf("ValidateFormat(%q) = %v, want %v", tc.val, err, tc.expectErr)
			}
		})
	}

	namingCases := []struct {
		expectErr error
		name      string
		val       string
	}{
		{name: "slug", val: NamingSlug, expectErr: nil},
		{name: "title", val: NamingTitle, expectErr: nil},
		{name: "custom invalid", val: "custom", expectErr: ErrInvalidNaming},
		{name: "empty invalid", val: "", expectErr: ErrInvalidNaming},
	}

	for _, tc := range namingCases {
		t.Run("Naming_"+tc.name, func(t *testing.T) {
			err := ValidateNaming(tc.val)
			if !errors.Is(err, tc.expectErr) {
				t.Fatalf("ValidateNaming(%q) = %v, want %v", tc.val, err, tc.expectErr)
			}
		})
	}
}

func TestNumericAndURLValidators(t *testing.T) {
	if err := ValidateConcurrency(1); err != nil {
		t.Fatalf("expected concurrency 1 to be valid, got %v", err)
	}
	if err := ValidateConcurrency(0); !errors.Is(err, ErrInvalidConcurrency) {
		t.Fatalf("expected ErrInvalidConcurrency, got %v", err)
	}

	if err := ValidateDelay(0); err != nil {
		t.Fatalf("expected delay 0 to be valid, got %v", err)
	}
	if err := ValidateDelay(-time.Millisecond); !errors.Is(err, ErrInvalidDelay) {
		t.Fatalf("expected ErrInvalidDelay, got %v", err)
	}

	if err := ValidateTimeout(time.Second); err != nil {
		t.Fatalf("expected timeout 1s to be valid, got %v", err)
	}
	if err := ValidateTimeout(0); !errors.Is(err, ErrInvalidTimeout) {
		t.Fatalf("expected ErrInvalidTimeout for 0, got %v", err)
	}

	if err := ValidateIndex(1); err != nil {
		t.Fatalf("expected index 1 to be valid, got %v", err)
	}
	if err := ValidateIndex(0); !errors.Is(err, ErrInvalidIndex) {
		t.Fatalf("expected ErrInvalidIndex for 0, got %v", err)
	}

	if err := ValidateStart(1); err != nil {
		t.Fatalf("expected start 1 to be valid, got %v", err)
	}
	if err := ValidateStart(0); !errors.Is(err, ErrInvalidStart) {
		t.Fatalf("expected ErrInvalidStart for 0, got %v", err)
	}

	if err := ValidateSeriesURL("https://www.webtoons.com/en/action/hero-a/list?title_no=100"); err != nil {
		t.Fatalf("expected valid webtoons URL, got %v", err)
	}
	if err := ValidateSeriesURL("https://example.com/en/action/hero-a/list?title_no=100"); !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("expected ErrInvalidURL, got %v", err)
	}
}

func TestValidateOptionConflicts(t *testing.T) {
	tests := []struct {
		expectErr     error
		name          string
		url           string
		index         int
		start         int
		explicitLinks bool
	}{
		{
			name:          "batch defaults",
			url:           "",
			index:         0,
			start:         0,
			explicitLinks: false,
			expectErr:     nil,
		},
		{
			name:          "index with start",
			url:           "",
			index:         2,
			start:         5,
			explicitLinks: true,
			expectErr:     nil,
		},
		{
			name:          "url with start",
			url:           "https://www.webtoons.com/en/action/hero-one/list?title_no=1",
			index:         0,
			start:         3,
			explicitLinks: false,
			expectErr:     nil,
		},
		{
			name:          "url with index conflict",
			url:           "https://www.webtoons.com/en/action/hero-two/list?title_no=2",
			index:         1,
			start:         0,
			explicitLinks: false,
			expectErr:     ErrConflictingOptions,
		},
		{
			name:          "url with explicit links conflict",
			url:           "https://www.webtoons.com/en/action/hero-three/list?title_no=3",
			index:         0,
			start:         0,
			explicitLinks: true,
			expectErr:     ErrConflictingOptions,
		},
		{
			name:          "start without url or index conflict",
			url:           "",
			index:         0,
			start:         2,
			explicitLinks: false,
			expectErr:     ErrConflictingOptions,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOptionConflicts(tc.url, tc.index, tc.start, tc.explicitLinks)
			if !errors.Is(err, tc.expectErr) {
				t.Fatalf("ValidateOptionConflicts() = %v, want %v", err, tc.expectErr)
			}
		})
	}
}

func TestValidateDirPath(t *testing.T) {
	origLstat := LstatFunc
	origAbs := AbsFunc
	t.Cleanup(func() {
		LstatFunc = origLstat
		AbsFunc = origAbs
	})

	rootDir := string(filepath.Separator)

	tests := []struct {
		expectErr error
		absFunc   func(string) (string, error)
		lstatFunc func(string) (os.FileInfo, error)
		name      string
		rawPath   string
	}{
		{
			name:      "empty path",
			rawPath:   "   ",
			expectErr: ErrInvalidPath,
		},
		{
			name:      "control character",
			rawPath:   "dir\x00evil",
			expectErr: ErrInvalidPath,
		},
		{
			name:      "del character",
			rawPath:   "dir\x7fevil",
			expectErr: ErrInvalidPath,
		},
		{
			name:      "dot dot traversal",
			rawPath:   "dir/../etc",
			expectErr: ErrPathTraversal,
		},
		{
			name:      "current directory dot",
			rawPath:   ".",
			expectErr: ErrInvalidPath,
		},
		{
			name:    "abs resolution failure",
			rawPath: "dir-abs-fail",
			absFunc: func(string) (string, error) {
				return "", errors.New("abs failed")
			},
			expectErr: ErrInvalidPath,
		},
		{
			name:    "root directory forbidden",
			rawPath: rootDir,
			absFunc: func(string) (string, error) {
				return rootDir, nil
			},
			expectErr: ErrInvalidPath,
		},
		{
			name:    "symlink in parent chain",
			rawPath: "parentA/targetA",
			absFunc: func(string) (string, error) {
				return filepath.Join(rootDir, "parentA", "targetA"), nil
			},
			lstatFunc: func(p string) (os.FileInfo, error) {
				if filepath.Base(p) == "parentA" {
					return mockFileInfo{name: filepath.Base(p), mode: os.ModeSymlink}, nil
				}
				return mockFileInfo{name: filepath.Base(p), mode: os.ModeDir, isDir: true}, nil
			},
			expectErr: ErrSymlinkForbidden,
		},
		{
			name:    "parent is regular file not dir",
			rawPath: "parentB/targetB",
			absFunc: func(string) (string, error) {
				return filepath.Join(rootDir, "parentB", "targetB"), nil
			},
			lstatFunc: func(p string) (os.FileInfo, error) {
				if filepath.Base(p) == "parentB" {
					return mockFileInfo{name: filepath.Base(p), mode: 0o644, isDir: false}, nil
				}
				return nil, os.ErrNotExist
			},
			expectErr: ErrInvalidPath,
		},
		{
			name:    "target exists as regular file",
			rawPath: "parentC/targetC",
			absFunc: func(string) (string, error) {
				return filepath.Join(rootDir, "parentC", "targetC"), nil
			},
			lstatFunc: func(p string) (os.FileInfo, error) {
				if filepath.Base(p) == "targetC" {
					return mockFileInfo{name: filepath.Base(p), mode: 0o644, isDir: false}, nil
				}
				return mockFileInfo{name: filepath.Base(p), mode: os.ModeDir, isDir: true}, nil
			},
			expectErr: ErrInvalidPath,
		},
		{
			name:    "lstat permission error",
			rawPath: "parentD/targetD",
			absFunc: func(string) (string, error) {
				return filepath.Join(rootDir, "parentD", "targetD"), nil
			},
			lstatFunc: func(string) (os.FileInfo, error) {
				return nil, os.ErrPermission
			},
			expectErr: ErrInvalidPath,
		},
		{
			name:    "valid existing directory",
			rawPath: "parentE/targetE",
			absFunc: func(string) (string, error) {
				return filepath.Join(rootDir, "parentE", "targetE"), nil
			},
			lstatFunc: func(p string) (os.FileInfo, error) {
				return mockFileInfo{name: filepath.Base(p), mode: os.ModeDir, isDir: true}, nil
			},
			expectErr: nil,
		},
		{
			name:    "valid non-existent target directory",
			rawPath: "parentF/new_downloads",
			absFunc: func(string) (string, error) {
				return filepath.Join(rootDir, "parentF", "new_downloads"), nil
			},
			lstatFunc: func(p string) (os.FileInfo, error) {
				if filepath.Base(p) == "new_downloads" {
					return nil, os.ErrNotExist
				}
				return mockFileInfo{name: filepath.Base(p), mode: os.ModeDir, isDir: true}, nil
			},
			expectErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.absFunc != nil {
				AbsFunc = tc.absFunc
			} else {
				AbsFunc = origAbs
			}
			if tc.lstatFunc != nil {
				LstatFunc = tc.lstatFunc
			} else {
				LstatFunc = origLstat
			}

			_, err := ValidateDirPath(tc.rawPath)
			if !errors.Is(err, tc.expectErr) {
				t.Fatalf("ValidateDirPath(%q) = %v, want %v", tc.rawPath, err, tc.expectErr)
			}
		})
	}
}

func TestValidateLinksFilePath(t *testing.T) {
	origLstat := LstatFunc
	origAbs := AbsFunc
	t.Cleanup(func() {
		LstatFunc = origLstat
		AbsFunc = origAbs
	})

	rootDir := string(filepath.Separator)

	tests := []struct {
		expectErr error
		absFunc   func(string) (string, error)
		lstatFunc func(string) (os.FileInfo, error)
		name      string
		rawPath   string
	}{
		{
			name:      "empty path",
			rawPath:   "",
			expectErr: ErrInvalidPath,
		},
		{
			name:      "dot path",
			rawPath:   ".",
			expectErr: ErrInvalidPath,
		},
		{
			name:    "abs failure",
			rawPath: "links-abs.txt",
			absFunc: func(string) (string, error) {
				return "", errors.New("abs failed")
			},
			expectErr: ErrInvalidPath,
		},
		{
			name:    "root path forbidden",
			rawPath: rootDir,
			absFunc: func(string) (string, error) {
				return rootDir, nil
			},
			expectErr: ErrInvalidPath,
		},
		{
			name:    "target is directory instead of regular file",
			rawPath: "folderA/links-dir.txt",
			absFunc: func(string) (string, error) {
				return filepath.Join(rootDir, "folderA", "links-dir.txt"), nil
			},
			lstatFunc: func(p string) (os.FileInfo, error) {
				return mockFileInfo{name: filepath.Base(p), mode: os.ModeDir, isDir: true}, nil
			},
			expectErr: ErrInvalidPath,
		},
		{
			name:    "valid regular file",
			rawPath: "folderB/links-ok.txt",
			absFunc: func(string) (string, error) {
				return filepath.Join(rootDir, "folderB", "links-ok.txt"), nil
			},
			lstatFunc: func(p string) (os.FileInfo, error) {
				if filepath.Base(p) == "links-ok.txt" {
					return mockFileInfo{name: filepath.Base(p), mode: 0o644, isDir: false}, nil
				}
				return mockFileInfo{name: filepath.Base(p), mode: os.ModeDir, isDir: true}, nil
			},
			expectErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.absFunc != nil {
				AbsFunc = tc.absFunc
			} else {
				AbsFunc = origAbs
			}
			if tc.lstatFunc != nil {
				LstatFunc = tc.lstatFunc
			} else {
				LstatFunc = origLstat
			}

			_, err := ValidateLinksFilePath(tc.rawPath)
			if !errors.Is(err, tc.expectErr) {
				t.Fatalf("ValidateLinksFilePath(%q) = %v, want %v", tc.rawPath, err, tc.expectErr)
			}
		})
	}
}

func TestValidateSubPath(t *testing.T) {
	origLstat := LstatFunc
	origAbs := AbsFunc
	t.Cleanup(func() {
		LstatFunc = origLstat
		AbsFunc = origAbs
	})

	rootDir := string(filepath.Separator)

	tests := []struct {
		expectErr error
		absFunc   func(string) (string, error)
		lstatFunc func(string) (os.FileInfo, error)
		name      string
		baseDir   string
		target    string
	}{
		{
			name:      "empty baseDir",
			baseDir:   "",
			target:    "baseA/subA",
			expectErr: ErrInvalidPath,
		},
		{
			name:      "empty target",
			baseDir:   "baseB",
			target:    "",
			expectErr: ErrInvalidPath,
		},
		{
			name:    "base abs error",
			baseDir: "baseC",
			target:  "baseC/subC",
			absFunc: func(p string) (string, error) {
				if p == "baseC" {
					return "", errors.New("base abs failed")
				}
				return p, nil
			},
			expectErr: ErrInvalidPath,
		},
		{
			name:    "target abs error",
			baseDir: "baseD",
			target:  "baseD/subD",
			absFunc: func(p string) (string, error) {
				if p != "baseD" {
					return "", errors.New("target abs failed")
				}
				return filepath.Join(rootDir, "baseD"), nil
			},
			expectErr: ErrInvalidPath,
		},
		{
			name:    "target equals baseDir",
			baseDir: "baseE",
			target:  "baseE",
			absFunc: func(string) (string, error) {
				return filepath.Join(rootDir, "baseE"), nil
			},
			expectErr: ErrPathTraversal,
		},
		{
			name:    "target outside baseDir",
			baseDir: "baseF",
			target:  "otherF/subF",
			absFunc: func(p string) (string, error) {
				return filepath.Join(rootDir, p), nil
			},
			expectErr: ErrPathTraversal,
		},
		{
			name:    "symlink inside subpath",
			baseDir: "baseG",
			target:  "baseG/seriesG/ch1.pdf",
			absFunc: func(p string) (string, error) {
				return filepath.Join(rootDir, p), nil
			},
			lstatFunc: func(p string) (os.FileInfo, error) {
				if filepath.Base(p) == "seriesG" {
					return mockFileInfo{name: filepath.Base(p), mode: os.ModeSymlink}, nil
				}
				return nil, os.ErrNotExist
			},
			expectErr: ErrSymlinkForbidden,
		},
		{
			name:    "valid nested subpath",
			baseDir: "baseH",
			target:  "baseH/seriesH/ch2.pdf",
			absFunc: func(p string) (string, error) {
				return filepath.Join(rootDir, p), nil
			},
			lstatFunc: func(string) (os.FileInfo, error) {
				return nil, os.ErrNotExist
			},
			expectErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.absFunc != nil {
				AbsFunc = tc.absFunc
			} else {
				AbsFunc = origAbs
			}
			if tc.lstatFunc != nil {
				LstatFunc = tc.lstatFunc
			} else {
				LstatFunc = origLstat
			}

			_, err := ValidateSubPath(tc.baseDir, tc.target)
			if !errors.Is(err, tc.expectErr) {
				t.Fatalf("ValidateSubPath(%q, %q) = %v, want %v", tc.baseDir, tc.target, err, tc.expectErr)
			}
		})
	}
}

func TestValidateWriteTarget(t *testing.T) {
	origLstat := LstatFunc
	t.Cleanup(func() {
		LstatFunc = origLstat
	})

	tests := []struct {
		expectErr error
		lstatFunc func(string) (os.FileInfo, error)
		name      string
		destPath  string
	}{
		{
			name:      "empty destination",
			destPath:  "",
			expectErr: ErrInvalidPath,
		},
		{
			name:      "dot destination",
			destPath:  ".",
			expectErr: ErrInvalidPath,
		},
		{
			name:     "parent directory is symlink",
			destPath: "outA/seriesA/chA.pdf",
			lstatFunc: func(p string) (os.FileInfo, error) {
				if filepath.Base(p) == "seriesA" {
					return mockFileInfo{name: filepath.Base(p), mode: os.ModeSymlink}, nil
				}
				return nil, os.ErrNotExist
			},
			expectErr: ErrSymlinkForbidden,
		},
		{
			name:     "destination file is symlink",
			destPath: "outB/seriesB/chB.pdf",
			lstatFunc: func(p string) (os.FileInfo, error) {
				if filepath.Base(p) == "chB.pdf" {
					return mockFileInfo{name: filepath.Base(p), mode: os.ModeSymlink}, nil
				}
				return mockFileInfo{name: filepath.Base(p), mode: os.ModeDir, isDir: true}, nil
			},
			expectErr: ErrSymlinkForbidden,
		},
		{
			name:     "temp file is symlink",
			destPath: "outC/seriesC/chC.pdf",
			lstatFunc: func(p string) (os.FileInfo, error) {
				if filepath.Base(p) == "chC.pdf.tmp" {
					return mockFileInfo{name: filepath.Base(p), mode: os.ModeSymlink}, nil
				}
				return nil, os.ErrNotExist
			},
			expectErr: ErrSymlinkForbidden,
		},
		{
			name:     "lstat error on write target",
			destPath: "outD/seriesD/chD.pdf",
			lstatFunc: func(string) (os.FileInfo, error) {
				return nil, os.ErrPermission
			},
			expectErr: ErrInvalidPath,
		},
		{
			name:     "valid write target",
			destPath: "outE/seriesE/chE.pdf",
			lstatFunc: func(string) (os.FileInfo, error) {
				return nil, os.ErrNotExist
			},
			expectErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.lstatFunc != nil {
				LstatFunc = tc.lstatFunc
			} else {
				LstatFunc = origLstat
			}

			_, err := ValidateWriteTarget(tc.destPath)
			if !errors.Is(err, tc.expectErr) {
				t.Fatalf("ValidateWriteTarget(%q) = %v, want %v", tc.destPath, err, tc.expectErr)
			}
		})
	}
}
