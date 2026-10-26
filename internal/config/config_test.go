package config_test

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/underhax/go-webtoons-downloader/internal/config"
	"github.com/underhax/go-webtoons-downloader/internal/validator"
)

func TestMain(m *testing.M) {
	origLstat := validator.LstatFunc
	validator.LstatFunc = func(string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	code := m.Run()
	validator.LstatFunc = origLstat
	os.Exit(code)
}

func TestParseDefaults(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	cfg, err := config.Parse([]string{}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Concurrency != 120 {
		t.Errorf("expected default concurrency 120, got %d", cfg.Concurrency)
	}
	if cfg.Delay != 0 {
		t.Errorf("expected default delay 0, got %v", cfg.Delay)
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("expected default timeout 30s, got %v", cfg.Timeout)
	}
	if cfg.Naming != "slug" {
		t.Errorf("expected default naming 'slug', got %s", cfg.Naming)
	}
	if cfg.Silent {
		t.Errorf("expected default silent to be false")
	}
	if cfg.SeriesJSON {
		t.Errorf("expected default series-json to be false")
	}
}

type flagsTestCase struct {
	expectErr   error
	name        string
	expectedDir string
	expectedFmt string
	expectedURL string
	args        []string
	expectedIdx int
	expectedSt  int
}

func TestParseFlagsTable(t *testing.T) {
	t.Parallel()

	tests := []flagsTestCase{
		{
			expectErr:   nil,
			name:        "custom directory and format cbz",
			expectedDir: "/mnt/data/webtoons",
			expectedFmt: "cbz",
			args:        []string{"--dir", "/mnt/data/webtoons", "--format", "cbz"},
		},
		{
			expectErr:   nil,
			name:        "short flags with index and start",
			expectedDir: "/var/webtoons",
			expectedFmt: "zip",
			expectedIdx: 5,
			expectedSt:  10,
			args:        []string{"-d", "/var/webtoons", "-f", "zip", "-i", "5", "-s", "10"},
		},
		{
			expectErr:   nil,
			name:        "single url with start chapter",
			expectedSt:  3,
			expectedURL: "https://www.webtoons.com/en/fantasy/tower/list?title_no=95",
			args:        []string{"--url", "https://www.webtoons.com/en/fantasy/tower/list?title_no=95", "--start", "3"},
		},
		{
			expectErr: config.ErrInvalidURL,
			name:      "invalid url failure",
			args:      []string{"--url", "https://example.com/trash"},
		},
		{
			expectErr: config.ErrInvalidURL,
			name:      "empty url flag failure",
			args:      []string{"--url="},
		},
		{
			expectErr: config.ErrInvalidFormat,
			name:      "invalid format failure",
			args:      []string{"--format", "invalid_format"},
		},
		{
			expectErr: config.ErrConflictingOptions,
			name:      "start without index or url failure",
			args:      []string{"--start", "2"},
		},
		{
			expectErr: config.ErrConflictingOptions,
			name:      "url with index failure",
			args:      []string{"-u", "https://www.webtoons.com/en/fantasy/tower-two/list?title_no=96", "--index", "1"},
		},
		{
			expectErr: config.ErrConflictingOptions,
			name:      "url with links failure",
			args:      []string{"-u", "https://www.webtoons.com/en/fantasy/tower-three/list?title_no=97", "--links", "custom.txt"},
		},
		{
			expectErr: config.ErrInvalidIndex,
			name:      "invalid index zero failure",
			args:      []string{"--index=0"},
		},
		{
			expectErr: config.ErrInvalidStart,
			name:      "invalid start zero failure",
			args:      []string{"--index=1", "--start=0"},
		},
		{
			expectErr: config.ErrInvalidDelay,
			name:      "negative delay failure",
			args:      []string{"--delay", "-50ms"},
		},
		{
			expectErr: config.ErrInvalidTimeout,
			name:      "zero timeout failure",
			args:      []string{"--timeout", "0s"},
		},
		{
			expectErr: config.ErrPathTraversal,
			name:      "directory traversal failure",
			args:      []string{"--dir", "../escape"},
		},
		{
			expectErr: config.ErrPathTraversal,
			name:      "links traversal failure",
			args:      []string{"--links", "../escape_links.txt"},
		},
		{
			expectErr: nil,
			name:      "help flag requested",
			args:      []string{"--help"},
		},
		{
			expectErr: nil,
			name:      "version flag requested",
			args:      []string{"--version"},
		},
		{
			expectErr: nil,
			name:      "update flag requested",
			args:      []string{"--update"},
		},
		{
			expectErr: config.ErrInvalidNaming,
			name:      "invalid naming failure",
			args:      []string{"--naming", "invalid_style"},
		},
		{
			expectErr: nil,
			name:      "naming title flag and silent flag",
			args:      []string{"--naming", config.NamingTitle, "--silent", "--delay", "500ms", "--timeout", "15s"},
		},
		{
			expectErr: config.ErrInvalidConcurrency,
			name:      "invalid concurrency failure",
			args:      []string{"--concurrency", "0"},
		},
		{
			expectErr: nil,
			name:      "series-json flag requested",
			args:      []string{"--series-json"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			cfg, err := config.Parse(tc.args, &buf)

			if tc.expectErr != nil {
				if !errors.Is(err, tc.expectErr) {
					t.Fatalf("expected error %v, got %v", tc.expectErr, err)
				}
				return
			}

			if tc.name == "help flag requested" {
				if err == nil {
					t.Fatalf("expected error for --help, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			assertConfigMatches(t, &tc, cfg)
		})
	}
}

func assertConfigMatches(t *testing.T, tc *flagsTestCase, cfg *config.Config) {
	t.Helper()

	expDir := tc.expectedDir
	if expDir == "" {
		expDir = "./data/webtoons"
	}
	expFmt := tc.expectedFmt
	if expFmt == "" {
		expFmt = "pdf"
	}

	if cfg.Directory != expDir {
		t.Errorf("expected dir %s, got %s", expDir, cfg.Directory)
	}
	if cfg.Format != expFmt {
		t.Errorf("expected format %s, got %s", expFmt, cfg.Format)
	}
	if cfg.Index != tc.expectedIdx {
		t.Errorf("expected index %d, got %d", tc.expectedIdx, cfg.Index)
	}
	if cfg.Start != tc.expectedSt {
		t.Errorf("expected start %d, got %d", tc.expectedSt, cfg.Start)
	}
	if tc.expectedURL != "" && cfg.URL != tc.expectedURL {
		t.Errorf("expected url %s, got %s", tc.expectedURL, cfg.URL)
	}

	assertSpecialFlags(t, tc, cfg)
}

func assertSpecialFlags(t *testing.T, tc *flagsTestCase, cfg *config.Config) {
	t.Helper()
	if tc.name == "version flag requested" && !cfg.Version {
		t.Errorf("expected Version to be true")
	}
	if tc.name == "update flag requested" && !cfg.Update {
		t.Errorf("expected Update to be true")
	}
	if tc.name == "series-json flag requested" && !cfg.SeriesJSON {
		t.Errorf("expected SeriesJSON to be true")
	}
	if tc.name == "naming title flag and silent flag" {
		if cfg.Naming != config.NamingTitle {
			t.Errorf("expected naming title, got %s", cfg.Naming)
		}
		if !cfg.Silent {
			t.Errorf("expected silent true, got false")
		}
	}
}

func TestParseEnvironmentVariables(t *testing.T) {
	t.Setenv("WEBTOONS_DIR", "/custom/env/dir")
	t.Setenv("WEBTOONS_LINKS", "/custom/env/links.txt")
	t.Setenv("WEBTOONS_FORMAT", "images")
	t.Setenv("WEBTOONS_INDEX", "7")
	t.Setenv("WEBTOONS_START", "12")
	t.Setenv("WEBTOONS_CONCURRENCY", "8")
	t.Setenv("WEBTOONS_DELAY", "250ms")
	t.Setenv("WEBTOONS_TIMEOUT", "45s")
	t.Setenv("WEBTOONS_NAMING", config.NamingTitle)
	t.Setenv("WEBTOONS_SILENT", "true")
	t.Setenv("WEBTOONS_SERIES_JSON", "true")

	var buf bytes.Buffer
	cfg, err := config.Parse([]string{}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Directory != "/custom/env/dir" {
		t.Errorf("expected dir from env, got %s", cfg.Directory)
	}
	if cfg.LinksFile != "/custom/env/links.txt" {
		t.Errorf("expected links from env, got %s", cfg.LinksFile)
	}
	if cfg.Format != config.FormatImages {
		t.Errorf("expected format images from env, got %s", cfg.Format)
	}
	if cfg.Index != 7 {
		t.Errorf("expected index 7 from env, got %d", cfg.Index)
	}
	if cfg.Start != 12 {
		t.Errorf("expected start 12 from env, got %d", cfg.Start)
	}
	if cfg.Concurrency != 8 {
		t.Errorf("expected concurrency 8 from env, got %d", cfg.Concurrency)
	}
	if cfg.Delay != 250*time.Millisecond {
		t.Errorf("expected delay 250ms from env, got %v", cfg.Delay)
	}
	if cfg.Timeout != 45*time.Second {
		t.Errorf("expected timeout 45s from env, got %v", cfg.Timeout)
	}
	if cfg.Naming != config.NamingTitle {
		t.Errorf("expected naming title from env, got %s", cfg.Naming)
	}
	if !cfg.Silent {
		t.Errorf("expected silent true from env, got false")
	}
	if !cfg.SeriesJSON {
		t.Errorf("expected series-json true from env, got false")
	}
}

func TestParseInvalidEnvironmentVariables(t *testing.T) {
	tests := []struct {
		expectErr error
		name      string
		envKey    string
		envVal    string
	}{
		{
			name:      "invalid concurrency syntax",
			envKey:    "WEBTOONS_CONCURRENCY",
			envVal:    "not_an_int",
			expectErr: config.ErrInvalidEnvVar,
		},
		{
			name:      "invalid concurrency value",
			envKey:    "WEBTOONS_CONCURRENCY",
			envVal:    "0",
			expectErr: config.ErrInvalidConcurrency,
		},
		{
			name:      "invalid index syntax",
			envKey:    "WEBTOONS_INDEX",
			envVal:    "bad_idx",
			expectErr: config.ErrInvalidEnvVar,
		},
		{
			name:      "invalid index value",
			envKey:    "WEBTOONS_INDEX",
			envVal:    "0",
			expectErr: config.ErrInvalidIndex,
		},
		{
			name:      "invalid start syntax",
			envKey:    "WEBTOONS_START",
			envVal:    "bad_start",
			expectErr: config.ErrInvalidEnvVar,
		},
		{
			name:      "invalid delay syntax",
			envKey:    "WEBTOONS_DELAY",
			envVal:    "bad_delay",
			expectErr: config.ErrInvalidEnvVar,
		},
		{
			name:      "negative delay in env",
			envKey:    "WEBTOONS_DELAY",
			envVal:    "-5s",
			expectErr: config.ErrInvalidDelay,
		},
		{
			name:      "invalid timeout syntax",
			envKey:    "WEBTOONS_TIMEOUT",
			envVal:    "bad_timeout",
			expectErr: config.ErrInvalidEnvVar,
		},
		{
			name:      "non-positive timeout in env",
			envKey:    "WEBTOONS_TIMEOUT",
			envVal:    "0s",
			expectErr: config.ErrInvalidTimeout,
		},
		{
			name:      "invalid silent bool syntax",
			envKey:    "WEBTOONS_SILENT",
			envVal:    "not_a_bool",
			expectErr: config.ErrInvalidEnvVar,
		},
		{
			name:      "invalid series-json bool syntax",
			envKey:    "WEBTOONS_SERIES_JSON",
			envVal:    "invalid_bool",
			expectErr: config.ErrInvalidEnvVar,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envKey, tc.envVal)

			var buf bytes.Buffer
			_, err := config.Parse([]string{}, &buf)
			if !errors.Is(err, tc.expectErr) {
				t.Fatalf("expected error %v for %s=%q, got %v", tc.expectErr, tc.envKey, tc.envVal, err)
			}
		})
	}
}

type failWriter struct{}

func (failWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("simulated write failure")
}

func TestPrintCustomUsageError(t *testing.T) {
	t.Parallel()

	_, err := config.Parse([]string{"--help"}, failWriter{})
	if err == nil {
		t.Fatalf("expected error on help flag with failWriter")
	}
}

func TestDefaultLinksPathFallback(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	var buf bytes.Buffer
	cfg, err := config.Parse([]string{}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.LinksFile == "" {
		t.Errorf("expected non-empty default links file")
	}
}

func TestDefaultLinksPathLocalExists(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := os.WriteFile("links.txt", []byte("https://example.org/series"), 0o600); err != nil {
		t.Fatalf("failed to create dummy links file: %v", err)
	}

	var buf bytes.Buffer
	cfg, err := config.Parse([]string{}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.LinksFile != "links.txt" {
		t.Errorf("expected local links.txt, got %s", cfg.LinksFile)
	}
}

func TestDefaultLinksPathNoUserConfig(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	var buf bytes.Buffer
	cfg, err := config.Parse([]string{}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.LinksFile != "links.txt" {
		t.Errorf("expected fallback links.txt, got %s", cfg.LinksFile)
	}
}
