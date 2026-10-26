package updater

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type updateTestCase struct {
	client      *http.Client
	name        string
	version     string
	errContains string
	wantErr     bool
}

func TestUpdate(t *testing.T) {
	dummyContent := "new-binary-content"
	validArchivePOSIX := createDummyTarGz(t, "wtd", dummyContent)
	validArchiveWin := createDummyZip(t, "wtd.exe", dummyContent)

	digestPOSIX := sha256.Sum256(validArchivePOSIX)
	digestWin := sha256.Sum256(validArchiveWin)
	digestPOSIXHex := "sha256:" + hex.EncodeToString(digestPOSIX[:])
	digestWinHex := "sha256:" + hex.EncodeToString(digestWin[:])

	validJSON := fmt.Sprintf(`{
		"tag_name": "v1.1.0",
		"assets": [
			{"name": "go-webtoons-downloader-linux-amd64.tar.gz", "browser_download_url": "https://example.com/dl/linux-amd64", "digest": "%s"},
			{"name": "go-webtoons-downloader-linux-arm64.tar.gz", "browser_download_url": "https://example.com/dl/linux-arm64", "digest": "%s"},
			{"name": "go-webtoons-downloader-macos-intel.tar.gz", "browser_download_url": "https://example.com/dl/macos-intel", "digest": "%s"},
			{"name": "go-webtoons-downloader-macos-apple-silicon.tar.gz", "browser_download_url": "https://example.com/dl/macos-silicon", "digest": "%s"},
			{"name": "go-webtoons-downloader-windows-x64.zip", "browser_download_url": "https://example.com/dl/windows-x64", "digest": "%s"},
			{"name": "go-webtoons-downloader-windows-arm64.zip", "browser_download_url": "https://example.com/dl/windows-arm64", "digest": "%s"}
		]
	}`, digestPOSIXHex, digestPOSIXHex, digestPOSIXHex, digestPOSIXHex, digestWinHex, digestWinHex)

	successClient := newMockClient(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.String(), "releases/latest") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(validJSON)),
			}, nil
		}
		if osGOOS == osWindows {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(validArchiveWin))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(validArchivePOSIX))}, nil
	})

	tests := []updateTestCase{
		{
			name:        "dev_version",
			version:     "dev",
			client:      nil,
			errContains: "update not available for development builds",
			wantErr:     true,
		},
		{
			name:    "already_up_to_date",
			version: "v1.1.0",
			client:  successClient,
			wantErr: false,
		},
		{
			name: "api_network_error",
			client: newMockClient(func(_ *http.Request) (*http.Response, error) {
				return nil, errors.New("network error")
			}),
			wantErr:     true,
			errContains: "network error fetching release",
		},
		{
			name: "api_bad_status",
			client: newMockClient(func(_ *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 404, Body: io.NopCloser(bytes.NewBufferString(""))}, nil
			}),
			wantErr:     true,
			errContains: "unexpected status from GitHub API",
		},
		{
			name: "api_bad_json",
			client: newMockClient(func(_ *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString("invalid json"))}, nil
			}),
			wantErr:     true,
			errContains: "failed to parse GitHub API response",
		},
		{
			name: "download_network_error",
			client: newMockClient(func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.String(), "releases/latest") {
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(validJSON))}, nil
				}
				return nil, errors.New("download net error")
			}),
			wantErr:     true,
			errContains: "network error downloading asset",
		},
		{
			name: "download_status_500",
			client: newMockClient(func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.String(), "releases/latest") {
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(validJSON))}, nil
				}
				return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(bytes.NewBufferString(""))}, nil
			}),
			wantErr:     true,
			errContains: "unexpected status downloading asset",
		},
		{
			name: "checksum_mismatch",
			client: newMockClient(func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.String(), "releases/latest") {
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(validJSON))}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString("corrupt-bytes"))}, nil
			}),
			wantErr:     true,
			errContains: "checksum mismatch",
		},
		{
			name:    "successful_update",
			client:  successClient,
			wantErr: false,
		},
	}

	origExec := osExecutable
	origEval := filepathEvalSymlinks
	defer func() {
		osExecutable = origExec
		filepathEvalSymlinks = origEval
	}()

	tempDir := t.TempDir()
	dummyExec := tempDir + "/webtoons-downloader"
	if err := os.WriteFile(dummyExec, []byte("old-binary"), 0o600); err != nil {
		t.Fatalf("failed to write dummy exec: %v", err)
	}

	osExecutable = func() (string, error) { return dummyExec, nil }
	filepathEvalSymlinks = func(p string) (string, error) { return p, nil }

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			targetVersion := tc.version
			if targetVersion == "" {
				targetVersion = "v1.0.0"
			}

			err := Update(ctx, tc.client, targetVersion)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("expected error to contain %q, got %q", tc.errContains, err.Error())
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestUnsupportedPlatform(t *testing.T) {
	origOS := osGOOS
	defer func() { osGOOS = origOS }()

	osGOOS = "plan9"
	err := Update(context.Background(), http.DefaultClient, "v1.0.0")
	if err == nil || !strings.Contains(err.Error(), "unsupported platform") {
		t.Errorf("expected unsupported platform error, got: %v", err)
	}
}

func mockExecLookupFail() (string, error) {
	return "", errors.New("exec lookup failed")
}

func mockExecLookupSuccess() (string, error) {
	return "/mock/bin", nil
}

func mockEvalSymlinksFail(_ string) (string, error) {
	return "", errors.New("eval symlinks failed")
}

func mockEvalSymlinksSuccess(p string) (string, error) {
	return p, nil
}

func mockRemoveFail(_ string) error {
	return errors.New("remove failure")
}

type failReadCloser struct{}

func (f *failReadCloser) Read(_ []byte) (int, error) {
	return 0, errors.New("simulated read failure")
}

func (f *failReadCloser) Close() error {
	return errors.New("simulated close failure")
}

var renameCallCount int

func mockRenameFailSecondCall(oldPath, newPath string) error {
	renameCallCount++
	if renameCallCount >= 2 {
		return errors.New("simulated rollback rename failure")
	}
	return os.Rename(oldPath, newPath)
}

func mockWriteFileFail(_ string, _ []byte, _ os.FileMode) error {
	return errors.New("simulated write file failure")
}

func mockFailingIOCopy(_ io.Writer, _ io.Reader) (int64, error) {
	return 0, errors.New("simulated io copy failure")
}

func mockFailingTempFileClose(_ *os.File) error {
	return errors.New("simulated temp file close failure")
}

func mockFailingZipOpen(_ *zip.File) (io.ReadCloser, error) {
	return nil, errors.New("simulated zip open failure")
}

func mockFailingGzipReaderClose(_ io.Reader) (io.ReadCloser, error) {
	return &failReadCloser{}, nil
}

func mockFailingNewRequest(_ context.Context, _, _ string, _ io.Reader) (*http.Request, error) {
	return nil, errors.New("simulated new request failure")
}

type customCloserReader struct {
	io.ReadCloser
}

func (c *customCloserReader) Close() error {
	if cerr := c.ReadCloser.Close(); cerr != nil {
		return fmt.Errorf("close wrapped reader: %w", cerr)
	}
	return errors.New("simulated close zip entry failure")
}

func mockZipOpenFailingClose(f *zip.File) (io.ReadCloser, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip file: %w", err)
	}
	return &customCloserReader{ReadCloser: rc}, nil
}

func TestUpdateMissingAssetAndChecksum(t *testing.T) {
	t.Run("asset not found in latest release", func(t *testing.T) {
		jsonWithoutMatchingAsset := `{"tag_name": "v1.1.0", "assets": [{"name": "unmatched-asset.tar.gz", "browser_download_url": "https://example.com/dl", "digest": "sha256:123"}]}`
		client := newMockClient(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(jsonWithoutMatchingAsset))}, nil
		})
		err := Update(context.Background(), client, "v1.0.0")
		if err == nil || !strings.Contains(err.Error(), "not found in latest release") {
			t.Errorf("expected asset not found error, got: %v", err)
		}
	})

	t.Run("checksum not found via github api", func(t *testing.T) {
		platformKey := fmt.Sprintf("%s/%s", osGOOS, runtime.GOARCH)
		assetName := SupportedPlatforms[platformKey]
		jsonWithoutDigest := fmt.Sprintf(`{"tag_name": "v1.1.0", "assets": [{"name": %q, "browser_download_url": "https://example.com/dl", "digest": ""}]}`, assetName)
		client := newMockClient(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(jsonWithoutDigest))}, nil
		})
		err := Update(context.Background(), client, "v1.0.0")
		if err == nil || !strings.Contains(err.Error(), "checksum for") {
			t.Errorf("expected checksum not found error, got: %v", err)
		}
	})
}

func TestUpdateExecutableErrors(t *testing.T) {
	dummyContent := "new-binary-content"
	validArchivePOSIX := createDummyTarGz(t, "wtd", dummyContent)
	digestPOSIX := sha256.Sum256(validArchivePOSIX)
	digestHex := "sha256:" + hex.EncodeToString(digestPOSIX[:])
	platformKey := fmt.Sprintf("%s/%s", osGOOS, runtime.GOARCH)
	assetName := SupportedPlatforms[platformKey]
	validJSON := fmt.Sprintf(`{"tag_name": "v1.1.0", "assets": [{"name": %q, "browser_download_url": "https://example.com/dl", "digest": %q}]}`, assetName, digestHex)

	client := newMockClient(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.String(), "releases/latest") {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(validJSON))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(validArchivePOSIX))}, nil
	})

	origExec := osExecutable
	origEval := filepathEvalSymlinks
	defer func() {
		osExecutable = origExec
		filepathEvalSymlinks = origEval
	}()

	osExecutable = mockExecLookupFail
	err := Update(context.Background(), client, "v1.0.0")
	if err == nil || !strings.Contains(err.Error(), "failed to determine executable path") {
		t.Errorf("expected exec lookup error, got: %v", err)
	}

	osExecutable = mockExecLookupSuccess
	filepathEvalSymlinks = mockEvalSymlinksFail
	err = Update(context.Background(), client, "v1.0.0")
	if err == nil || !strings.Contains(err.Error(), "failed to evaluate symlinks") {
		t.Errorf("expected symlink eval error, got: %v", err)
	}
}

func TestDefaultHelpers(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "dummy")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("failed to close temp file: %v", err)
	}
	if err := defaultTempFileClose(f); err == nil {
		t.Errorf("expected error closing already closed file")
	}

	if _, err := defaultNewGzipReader(bytes.NewReader([]byte("not-gzip"))); err == nil {
		t.Errorf("expected error from defaultNewGzipReader with invalid gzip")
	}
}

func TestCleanupWindowsOldFilesBranches(t *testing.T) {
	t.Helper()

	origOS := osGOOS
	origExec := osExecutable
	origEval := filepathEvalSymlinks
	origRemove := osRemove
	defer func() {
		osGOOS = origOS
		osExecutable = origExec
		filepathEvalSymlinks = origEval
		osRemove = origRemove
	}()

	osGOOS = osWindows

	osExecutable = mockExecLookupFail
	CleanupWindowsOldFiles()

	osExecutable = mockExecLookupSuccess
	filepathEvalSymlinks = mockEvalSymlinksFail
	CleanupWindowsOldFiles()

	filepathEvalSymlinks = mockEvalSymlinksSuccess
	osRemove = mockRemoveFail
	CleanupWindowsOldFiles()
}

var mockCustomExecPath string

func mockCustomExecLookup() (string, error) {
	return mockCustomExecPath, nil
}

func TestDefaultZipOpenUnsupportedAlgorithm(t *testing.T) {
	t.Parallel()

	dummyZip := createDummyZip(t, "file.txt", "content")
	zr, err := zip.NewReader(bytes.NewReader(dummyZip), int64(len(dummyZip)))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}

	zr.File[0].Method = 9999
	_, err = defaultZipOpen(zr.File[0])
	if err == nil || !strings.Contains(err.Error(), "zip open error") {
		t.Errorf("expected zip open error for method 9999, got: %v", err)
	}
}

func TestDefaultIOCopy(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	n, err := defaultIOCopy(&buf, strings.NewReader("hello"))
	if err != nil || n != 5 {
		t.Errorf("expected 5 bytes copied, got %d, err: %v", n, err)
	}

	_, err = defaultIOCopy(&buf, &failReadCloser{})
	if err == nil {
		t.Errorf("expected error from defaultIOCopy with failing reader, got nil")
	}
}

func TestFetchLatestReleaseAndDownloadAssetErrors(t *testing.T) {
	t.Parallel()

	t.Run("fetchLatestRelease new request failure", func(t *testing.T) {
		origReq := newRequestWithContext
		defer func() { newRequestWithContext = origReq }()
		newRequestWithContext = mockFailingNewRequest

		client := &http.Client{}
		_, err := fetchLatestRelease(context.Background(), client)
		if err == nil || !strings.Contains(err.Error(), "failed to create request") {
			t.Errorf("expected failed to create request error, got: %v", err)
		}
	})

	t.Run("fetchLatestRelease defer close error", func(t *testing.T) {
		client := newMockClient(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       &failReadCloser{},
			}, nil
		})
		_, err := fetchLatestRelease(context.Background(), client)
		if err == nil {
			t.Errorf("expected json decode error from failReadCloser, got nil")
		}
	})

	t.Run("downloadAndVerifyAsset new request failure", func(t *testing.T) {
		origReq := newRequestWithContext
		defer func() { newRequestWithContext = origReq }()
		newRequestWithContext = mockFailingNewRequest

		client := &http.Client{}
		_, err := downloadAndVerifyAsset(context.Background(), client, "https://example.com/asset", "sha256:123")
		if err == nil || !strings.Contains(err.Error(), "failed to create download request") {
			t.Errorf("expected failed to create download request error, got: %v", err)
		}
	})

	t.Run("downloadAndVerifyAsset read failure and defer close error", func(t *testing.T) {
		client := newMockClient(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       &failReadCloser{},
			}, nil
		})
		_, err := downloadAndVerifyAsset(context.Background(), client, "https://example.com/asset", "sha256:123")
		if err == nil || !strings.Contains(err.Error(), "failed to read downloaded asset") {
			t.Errorf("expected failed to read downloaded asset error, got: %v", err)
		}
	})
}

func TestReplacePOSIXCorruptedTarAndDeferClose(t *testing.T) {
	t.Run("corrupted tar archive", func(t *testing.T) {
		var buf bytes.Buffer
		gzw := gzip.NewWriter(&buf)
		if _, err := gzw.Write([]byte("not-a-tar-header-corrupted-data")); err != nil {
			t.Fatalf("write gzip: %v", err)
		}
		if err := gzw.Close(); err != nil {
			t.Fatalf("close gzip: %v", err)
		}

		err := replacePOSIX(&buf, "/mock/bin")
		if err == nil || !strings.Contains(err.Error(), "failed to read tar archive") {
			t.Errorf("expected tar read error, got: %v", err)
		}
	})

	t.Run("gzip reader close defer error", func(t *testing.T) {
		origGzip := newGzipReader
		defer func() { newGzipReader = origGzip }()
		newGzipReader = mockFailingGzipReaderClose

		if err := replacePOSIX(strings.NewReader("data"), "/mock/bin"); err == nil {
			t.Errorf("expected error, got nil")
		}
	})
}

func TestReplaceWindowsErrors(t *testing.T) {
	origZipOpen := zipOpen
	defer func() { zipOpen = origZipOpen }()

	dummyZip := createDummyZip(t, "wtd.exe", "content")

	t.Run("zip read body failure", func(t *testing.T) {
		err := replaceWindows(&failReadCloser{}, "/mock/bin.exe")
		if err == nil || !strings.Contains(err.Error(), "failed to read zip body") {
			t.Errorf("expected read zip body error, got: %v", err)
		}
	})

	t.Run("zipOpen failure", func(t *testing.T) {
		zipOpen = mockFailingZipOpen
		err := replaceWindows(bytes.NewReader(dummyZip), "/mock/bin.exe")
		zipOpen = origZipOpen
		if err == nil || !strings.Contains(err.Error(), "failed to open executable in zip") {
			t.Errorf("expected zipOpen error, got: %v", err)
		}
	})

	t.Run("rc.Close failure after replace", func(t *testing.T) {
		tempDir := t.TempDir()
		execPath := filepath.Join(tempDir, "bin.exe")
		if err := os.WriteFile(execPath, []byte("old"), 0o600); err != nil {
			t.Fatalf("write exec: %v", err)
		}

		zipOpen = mockZipOpenFailingClose
		err := replaceWindows(bytes.NewReader(dummyZip), execPath)
		zipOpen = origZipOpen
		if err == nil || !strings.Contains(err.Error(), "failed to close zip entry") {
			t.Errorf("expected failed to close zip entry error, got: %v", err)
		}
	})
}

func TestDefaultNewRequestWithContextError(t *testing.T) {
	t.Parallel()

	_, err := defaultNewRequestWithContext(context.Background(), "invalid method with spaces", "https://example.com", nil)
	if err == nil {
		t.Errorf("expected error from defaultNewRequestWithContext with invalid method, got nil")
	}
}

func TestPerformAtomicSwapErrors(t *testing.T) {
	origIOCopy := ioCopy
	origTempClose := tempFileClose
	defer func() {
		ioCopy = origIOCopy
		tempFileClose = origTempClose
	}()

	tempDir := t.TempDir()
	execPath := filepath.Join(tempDir, "bin")

	t.Run("ioCopy error and tempFileClose error", func(t *testing.T) {
		ioCopy = mockFailingIOCopy
		tempFileClose = mockFailingTempFileClose
		err := performAtomicSwap(strings.NewReader("data"), execPath)
		if err == nil || !strings.Contains(err.Error(), "failed to write new binary") {
			t.Errorf("expected joined write error, got: %v", err)
		}
	})

	t.Run("ioCopy error only", func(t *testing.T) {
		ioCopy = mockFailingIOCopy
		tempFileClose = defaultTempFileClose
		err := performAtomicSwap(strings.NewReader("data"), execPath)
		if err == nil || !strings.Contains(err.Error(), "failed to write new binary") {
			t.Errorf("expected write error, got: %v", err)
		}
	})

	t.Run("tempFileClose error after copy", func(t *testing.T) {
		ioCopy = defaultIOCopy
		tempFileClose = mockFailingTempFileClose
		err := performAtomicSwap(strings.NewReader("data"), execPath)
		if err == nil || !strings.Contains(err.Error(), "close temp file") {
			t.Errorf("expected close temp file error, got: %v", err)
		}
	})

	t.Run("defer osRemove error", func(t *testing.T) {
		origRemove := osRemove
		defer func() { osRemove = origRemove }()
		osRemove = mockRemoveFail
		if err := performAtomicSwap(&failReadCloser{}, execPath); err == nil {
			t.Errorf("expected error from performAtomicSwap with failReadCloser, got nil")
		}
	})
}

func TestPerformWindowsReplaceErrors(t *testing.T) {
	origRemove := osRemove
	origRename := osRename
	origWriteFile := osWriteFile
	defer func() {
		osRemove = origRemove
		osRename = origRename
		osWriteFile = origWriteFile
	}()

	tempDir := t.TempDir()
	execPath := filepath.Join(tempDir, "bin.exe")
	oldPath := execPath + ".old"

	t.Run("remove old binary failure", func(t *testing.T) {
		if err := os.WriteFile(oldPath, []byte("old"), 0o600); err != nil {
			t.Fatalf("write old: %v", err)
		}
		osRemove = mockRemoveFail
		err := performWindowsReplace(strings.NewReader("new"), execPath)
		osRemove = origRemove
		if err == nil || !strings.Contains(err.Error(), "remove old binary") {
			t.Errorf("expected remove old binary error, got: %v", err)
		}
	})

	t.Run("read all src error with rollback rename success", func(t *testing.T) {
		if err := os.WriteFile(execPath, []byte("current"), 0o600); err != nil {
			t.Fatalf("write current: %v", err)
		}
		osRemove = os.Remove
		osRename = os.Rename
		err := performWindowsReplace(&failReadCloser{}, execPath)
		if err == nil || !strings.Contains(err.Error(), "failed to read new binary from archive") {
			t.Errorf("expected read binary error, got: %v", err)
		}
	})

	t.Run("read all src error joined with rollback rename error", func(t *testing.T) {
		if err := os.WriteFile(execPath, []byte("current"), 0o600); err != nil {
			t.Fatalf("write current: %v", err)
		}
		osRemove = os.Remove
		renameCallCount = 0
		osRename = mockRenameFailSecondCall
		err := performWindowsReplace(&failReadCloser{}, execPath)
		osRename = origRename
		if err == nil || !strings.Contains(err.Error(), "simulated rollback rename failure") {
			t.Errorf("expected read binary joined error, got: %v", err)
		}
	})

	t.Run("write file error joined with rollback rename error", func(t *testing.T) {
		if err := os.WriteFile(execPath, []byte("current"), 0o600); err != nil {
			t.Fatalf("write current: %v", err)
		}
		osRemove = os.Remove
		osWriteFile = mockWriteFileFail
		renameCallCount = 0
		osRename = mockRenameFailSecondCall
		err := performWindowsReplace(strings.NewReader("new"), execPath)
		osWriteFile = origWriteFile
		osRename = origRename
		if err == nil || !strings.Contains(err.Error(), "simulated rollback rename failure") {
			t.Errorf("expected write file joined error, got: %v", err)
		}
	})
}

func TestUpdateWindowsSuccess(t *testing.T) {
	origOS := osGOOS
	origExec := osExecutable
	origEval := filepathEvalSymlinks
	defer func() {
		osGOOS = origOS
		osExecutable = origExec
		filepathEvalSymlinks = origEval
	}()

	osGOOS = osWindows
	tempDir := t.TempDir()
	fakeExec := filepath.Join(tempDir, "wtd.exe")
	if err := os.WriteFile(fakeExec, []byte("old-exe"), 0o600); err != nil {
		t.Fatalf("write fake exec: %v", err)
	}

	mockCustomExecPath = fakeExec
	osExecutable = mockCustomExecLookup
	filepathEvalSymlinks = mockEvalSymlinksSuccess

	validArchiveWin := createDummyZip(t, "wtd.exe", "new-exe")
	digestWin := sha256.Sum256(validArchiveWin)
	digestWinHex := "sha256:" + hex.EncodeToString(digestWin[:])

	platformKey := "windows/" + runtime.GOARCH
	assetName := SupportedPlatforms[platformKey]
	validJSON := fmt.Sprintf(`{"tag_name": "v1.1.0", "assets": [{"name": %q, "browser_download_url": "https://example.com/dl", "digest": %q}]}`, assetName, digestWinHex)

	client := newMockClient(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.String(), "releases/latest") {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(validJSON))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(validArchiveWin))}, nil
	})

	err := Update(context.Background(), client, "v1.0.0")
	if err != nil {
		t.Errorf("expected successful windows update, got: %v", err)
	}
}

func TestUpdaterReadBoundedBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		errExpected error
		name        string
		content     string
		maxBytes    int64
	}{
		{
			name:        "within bounds succeeds",
			content:     "small payload",
			maxBytes:    100,
			errExpected: nil,
		},
		{
			name:        "exceeds bounds returns ErrResponseTooLarge",
			content:     "payload exceeding limit",
			maxBytes:    10,
			errExpected: ErrResponseTooLarge,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := strings.NewReader(tc.content)
			data, err := readBoundedBody(r, tc.maxBytes)
			if tc.errExpected != nil {
				if !errors.Is(err, tc.errExpected) {
					t.Fatalf("expected error %v, got %v", tc.errExpected, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(data) != tc.content {
				t.Errorf("expected %q, got %q", tc.content, string(data))
			}
		})
	}
}
