package syncer_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/underhax/go-webtoons-downloader/internal/syncer"
)

func TestParseLinksTable(t *testing.T) {
	t.Parallel()

	validLinksContent := `
# Comment line to ignore
https://www.webtoons.com/en/fantasy/tower/list?title_no=95
""
'' # empty quoted comment
https://invalid-domain.com/trash
https://www.webtoons.com/en/fantasy/invalid-missing-title-no/list
https://m.webtoons.com/en/action/unordinary/list?title_no=184 # inline comment
# Another comment
webtoons.com/zh-hant/romance/lore/list?title_no=95
`

	tests := []struct {
		expectErr       error
		name            string
		input           string
		expectedEntries []syncer.LinkEntry
	}{
		{
			expectErr: nil,
			name:      "valid sequential links with comments and empty lines",
			input:     validLinksContent,
			expectedEntries: []syncer.LinkEntry{
				{Index: 1, URL: "https://www.webtoons.com/en/fantasy/tower/list?title_no=95"},
				{Index: 2, URL: "https://m.webtoons.com/en/action/unordinary/list?title_no=184"},
				{Index: 3, URL: "webtoons.com/zh-hant/romance/lore/list?title_no=95"},
			},
		},
		{
			expectErr:       syncer.ErrEmptyLinksFile,
			name:            "only comments and whitespace rejected",
			input:           "# comment\n\n   \n",
			expectedEntries: nil,
		},
		{
			expectErr:       syncer.ErrEmptyLinksFile,
			name:            "only invalid urls rejected",
			input:           "https://google.com/test\nnot-a-valid-url\nhttps://www.webtoons.com/viewer?bad=1\n",
			expectedEntries: nil,
		},
		{
			expectErr: nil,
			name:      "inline quotes with hash",
			input:     `"https://www.webtoons.com/fr/comedy/frog/list?title_no=101" # comment` + "\n" + `'https://m.webtoons.com/fr/canvas/my-comic/list?title_no=123' # comment`,
			expectedEntries: []syncer.LinkEntry{
				{Index: 1, URL: "https://www.webtoons.com/fr/comedy/frog/list?title_no=101"},
				{Index: 2, URL: "https://m.webtoons.com/fr/canvas/my-comic/list?title_no=123"},
			},
		},
		{
			expectErr:       syncer.ErrEmptyLinksFile,
			name:            "empty content rejected",
			input:           "",
			expectedEntries: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entries, err := syncer.ParseLinks(strings.NewReader(tc.input))
			if tc.expectErr != nil {
				if !errors.Is(err, tc.expectErr) {
					t.Fatalf("expected error %v, got %v", tc.expectErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(entries) != len(tc.expectedEntries) {
				t.Fatalf("expected %d entries, got %d", len(tc.expectedEntries), len(entries))
			}

			for i, exp := range tc.expectedEntries {
				if entries[i].Index != exp.Index || entries[i].URL != exp.URL {
					t.Errorf("entry %d mismatch: got %+v, want %+v", i, entries[i], exp)
				}
			}
		})
	}
}

func TestDetectLastChapterAndFolderName(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	folderName := syncer.BuildFolderName(1, "Tower of God")
	if folderName != "001. Tower of God" {
		t.Errorf("expected '001. Tower of God', got '%s'", folderName)
	}

	targetDir := filepath.Join(tempDir, folderName)
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatalf("failed to create target dir: %v", err)
	}

	filesToCreate := []string{"001.pdf", "002.pdf", "042.pdf", "readme.txt"}
	for _, f := range filesToCreate {
		filePath := filepath.Join(targetDir, f)
		if err := os.WriteFile(filePath, []byte("data"), 0o600); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}
	}

	lastChapter, err := syncer.DetectLastChapter(targetDir, ".pdf")
	if err != nil {
		t.Fatalf("unexpected error detecting last chapter: %v", err)
	}
	if lastChapter != 42 {
		t.Errorf("expected last chapter 42, got %d", lastChapter)
	}

	nonExistentDir := filepath.Join(tempDir, "99. NonExistent")
	zeroChapter, err := syncer.DetectLastChapter(nonExistentDir, ".pdf")
	if err != nil {
		t.Fatalf("unexpected error on missing dir: %v", err)
	}
	if zeroChapter != 0 {
		t.Errorf("expected 0 for missing dir, got %d", zeroChapter)
	}
}

func TestBuildFolderNameSanitizationTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rawTitle string
		expected string
		index    int
	}{
		{
			name:     "path traversal slashes and dots neutralized",
			index:    1,
			rawTitle: "../../etc/passwd",
			expected: "001. etc - passwd",
		},
		{
			name:     "colons and slashes converted to clean hyphens",
			index:    2,
			rawTitle: "Fate/stay: night",
			expected: "002. Fate - stay - night",
		},
		{
			name:     "wildcards stripped and quotes converted",
			index:    3,
			rawTitle: `Is It "Wrong"? *`,
			expected: "003. Is It 'Wrong'",
		},
		{
			name:     "angle brackets and pipes converted and trimmed",
			index:    4,
			rawTitle: "<The Hero | Legend>",
			expected: "004. The Hero - Legend",
		},
		{
			name:     "control characters stripped",
			index:    5,
			rawTitle: "Title\x00\x1b\r\nEnd",
			expected: "005. TitleEnd",
		},
		{
			name:     "trailing dots and spaces trimmed",
			index:    6,
			rawTitle: "Webtoon Comic...   ",
			expected: "006. Webtoon Comic",
		},
		{
			name:     "pure dots fallback to default",
			index:    7,
			rawTitle: "...",
			expected: "007. webtoon",
		},
		{
			name:     "windows reserved device name escaped",
			index:    8,
			rawTitle: "NUL",
			expected: "008. NUL_",
		},
		{
			name:     "zero index without prefix",
			index:    0,
			rawTitle: "Direct/Title",
			expected: "Direct - Title",
		},
		{
			name:     "long title truncated to byte limit",
			index:    10,
			rawTitle: strings.Repeat("a", 300),
			expected: "010. " + strings.Repeat("a", 240),
		},
		{
			index:    11,
			name:     "multiple hyphens collapsed and trimmed",
			rawTitle: "- - Title - - End -",
			expected: "011. Title - End",
		},
		{
			index:    12,
			name:     "empty title fallback",
			rawTitle: "",
			expected: "012. webtoon",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res := syncer.BuildFolderName(tc.index, tc.rawTitle)
			if res != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, res)
			}
		})
	}
}

func TestParseLinksFile(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	validPath := filepath.Join(tempDir, "valid_links.txt")
	if err := os.WriteFile(validPath, []byte("https://www.webtoons.com/es/drama/hero/list?title_no=103\n"), 0o600); err != nil {
		t.Fatalf("failed to create valid links file: %v", err)
	}

	entries, err := syncer.ParseLinksFile(validPath)
	if err != nil {
		t.Fatalf("unexpected error parsing links file: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}

	missingPath := filepath.Join(tempDir, "non_existent_links.txt")
	if _, err := syncer.ParseLinksFile(missingPath); err == nil {
		t.Fatalf("expected error parsing non existent links file")
	}

	if _, err := syncer.ParseLinksFile("../escape_links.txt"); err == nil {
		t.Fatalf("expected error parsing traversal links file path")
	}
}
