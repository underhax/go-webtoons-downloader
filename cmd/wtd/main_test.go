package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/underhax/go-webtoons-downloader/internal/config"
	"github.com/underhax/go-webtoons-downloader/internal/validator"
	"github.com/underhax/go-webtoons-downloader/internal/webtoon"
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

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write failure")
}

func mockRunSuccess(_ []string) error {
	return nil
}

func mockRunHelp(_ []string) error {
	return flag.ErrHelp
}

func mockRunFailure(_ []string) error {
	return errors.New("simulated run failure")
}

func mockUpdateSuccess(_ context.Context, _ *http.Client, _ string) error {
	return nil
}

func mockUpdateFailure(_ context.Context, _ *http.Client, _ string) error {
	return errors.New("simulated update failure")
}

func mockSyncSuccess(_ context.Context, _ *config.Config, _ *webtoon.Client, _ io.Writer) error {
	return nil
}

func mockSyncFailure(_ context.Context, _ *config.Config, _ *webtoon.Client, _ io.Writer) error {
	return errors.New("simulated sync failure")
}

var (
	lastExitCode   int
	lastExitCalled bool
)

func mockExit(code int) {
	lastExitCalled = true
	lastExitCode = code
}

func TestMainFunctionTable(t *testing.T) {
	origArgs := os.Args
	origRun := runFunc
	origExit := exitFunc
	origStderr := stderrWriter
	t.Cleanup(func() {
		os.Args = origArgs
		runFunc = origRun
		exitFunc = origExit
		stderrWriter = origStderr
	})

	tests := []struct {
		runFn    func([]string) error
		stderr   io.Writer
		name     string
		expected int
		exited   bool
	}{
		{
			runFn:    mockRunSuccess,
			stderr:   io.Discard,
			name:     "run succeeds without exit",
			expected: 0,
			exited:   false,
		},
		{
			runFn:    mockRunHelp,
			stderr:   io.Discard,
			name:     "run returns help exits zero",
			expected: 0,
			exited:   true,
		},
		{
			runFn:    mockRunFailure,
			stderr:   io.Discard,
			name:     "run returns error exits one",
			expected: 1,
			exited:   true,
		},
		{
			runFn:    mockRunFailure,
			stderr:   failingWriter{},
			name:     "run returns error with failing stderr exits one",
			expected: 1,
			exited:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lastExitCalled = false
			lastExitCode = -1
			exitFunc = mockExit
			runFunc = tc.runFn
			stderrWriter = tc.stderr
			os.Args = []string{"wtd"}

			main()

			if lastExitCalled != tc.exited {
				t.Fatalf("expected exitCalled=%v, got %v", tc.exited, lastExitCalled)
			}
			if lastExitCalled && lastExitCode != tc.expected {
				t.Fatalf("expected exitCode=%d, got %d", tc.expected, lastExitCode)
			}
		})
	}
}

func TestRunScenarios(t *testing.T) {
	origUpdate := updateFunc
	origSync := executeSyncFunc
	origStdout := stdoutWriter
	t.Cleanup(func() {
		updateFunc = origUpdate
		executeSyncFunc = origSync
		stdoutWriter = origStdout
	})

	tests := []struct {
		updateFn  func(context.Context, *http.Client, string) error
		syncFn    func(context.Context, *config.Config, *webtoon.Client, io.Writer) error
		stdout    io.Writer
		name      string
		args      []string
		expectErr bool
	}{
		{
			updateFn:  mockUpdateSuccess,
			syncFn:    mockSyncSuccess,
			stdout:    io.Discard,
			name:      "version requested",
			args:      []string{"--version"},
			expectErr: false,
		},
		{
			updateFn:  mockUpdateSuccess,
			syncFn:    mockSyncSuccess,
			stdout:    failingWriter{},
			name:      "version stdout error",
			args:      []string{"-v"},
			expectErr: true,
		},
		{
			updateFn:  mockUpdateSuccess,
			syncFn:    mockSyncSuccess,
			stdout:    io.Discard,
			name:      "update succeeds",
			args:      []string{"--update"},
			expectErr: false,
		},
		{
			updateFn:  mockUpdateFailure,
			syncFn:    mockSyncSuccess,
			stdout:    io.Discard,
			name:      "update fails",
			args:      []string{"--update"},
			expectErr: true,
		},
		{
			updateFn:  mockUpdateSuccess,
			syncFn:    mockSyncSuccess,
			stdout:    io.Discard,
			name:      "sync succeeds with silent",
			args:      []string{"--silent", "--url", "https://www.webtoons.com/en/fantasy/tower/list?title_no=95"},
			expectErr: false,
		},
		{
			updateFn:  mockUpdateSuccess,
			syncFn:    mockSyncFailure,
			stdout:    io.Discard,
			name:      "sync fails",
			args:      []string{"--url", "https://www.webtoons.com/en/action/unordinary/list?title_no=184"},
			expectErr: true,
		},
		{
			updateFn:  mockUpdateSuccess,
			syncFn:    mockSyncSuccess,
			stdout:    io.Discard,
			name:      "invalid flags",
			args:      []string{"--format", "invalid_choice"},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			updateFunc = tc.updateFn
			executeSyncFunc = tc.syncFn
			stdoutWriter = tc.stdout

			err := run(tc.args)
			if (err != nil) != tc.expectErr {
				t.Fatalf("expected error=%v, got %v", tc.expectErr, err)
			}
		})
	}
}

func createMockResponse(contentType, body string) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestDefaults(t *testing.T) {
	t.Run("defaultRun executes run", func(t *testing.T) {
		origStdout := stdoutWriter
		t.Cleanup(func() { stdoutWriter = origStdout })
		stdoutWriter = io.Discard

		if err := defaultRun([]string{"-v"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("defaultUpdate with current version succeeds without network", func(t *testing.T) {
		mockClient := &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return createMockResponse("application/json", `{"tag_name":"v1.0.0","assets":[]}`), nil
			}),
		}

		if err := defaultUpdate(context.Background(), mockClient, "v1.0.0"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("defaultUpdate with canceled context fails without network", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if err := defaultUpdate(ctx, http.DefaultClient, "dev"); err == nil {
			t.Fatalf("expected error with canceled context, got nil")
		}
	})

	t.Run("defaultExecuteSync with invalid format fails syncer init", func(t *testing.T) {
		cfg := &config.Config{
			Format:  "invalid_format",
			Timeout: 5 * time.Second,
		}
		client := webtoon.NewClient(5 * time.Second)

		if err := defaultExecuteSync(context.Background(), cfg, client, io.Discard); err == nil {
			t.Fatalf("expected error initializing syncer with invalid format")
		}
	})

	t.Run("defaultExecuteSync with canceled context fails execution", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		cfg := &config.Config{
			Directory:   t.TempDir(),
			LinksFile:   filepath.Join(t.TempDir(), "links.txt"),
			Format:      config.FormatPDF,
			Concurrency: 1,
			Timeout:     5 * time.Second,
		}
		client := webtoon.NewClient(5 * time.Second)

		if err := defaultExecuteSync(ctx, cfg, client, io.Discard); err == nil {
			t.Fatalf("expected error executing syncer with canceled context")
		}
	})

	t.Run("defaultExecuteSync succeeds with mock webtoon client", func(t *testing.T) {
		mockWebtoonHTTP := &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/en/fantasy/tower/list" {
					body := `<html><head><link rel="canonical" href="https://www.webtoons.com/en/fantasy/tower/list?title_no=95"><meta property="og:title" content="Tower of God"></head></html>`
					return createMockResponse("text/html", body), nil
				}
				if req.URL.Path == "/api/v1/webtoon/95/episodes" {
					body := `{"result":{"episodeList":[{"episodeNo":1,"episodeTitle":"Episode 1","viewerLink":"/en/fantasy/tower/viewer?episode_no=1"}]}}`
					return createMockResponse("application/json", body), nil
				}
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("not found")),
				}, nil
			}),
		}

		client := webtoon.NewClient(
			5*time.Second,
			webtoon.WithBaseURLs("https://www.webtoons.com", "https://m.webtoons.com"),
			webtoon.WithHTTPClient(mockWebtoonHTTP),
		)

		tempDir := t.TempDir()
		seriesDir := filepath.Join(tempDir, "tower")
		if err := os.MkdirAll(seriesDir, 0o700); err != nil {
			t.Fatalf("mkdir series dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(seriesDir, "001.pdf"), []byte("dummy pdf content"), 0o600); err != nil {
			t.Fatalf("write existing chapter: %v", err)
		}

		cfg := &config.Config{
			Directory:   tempDir,
			URL:         "https://www.webtoons.com/en/fantasy/tower/list?title_no=95",
			Format:      config.FormatPDF,
			Concurrency: 1,
			Timeout:     5 * time.Second,
			Silent:      true,
		}

		if err := defaultExecuteSync(context.Background(), cfg, client, io.Discard); err != nil {
			t.Fatalf("unexpected error executing syncer: %v", err)
		}
	})
}
