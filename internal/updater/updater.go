// Package updater provides in-place self-update functionality via GitHub Releases.
package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Endpoints and sentinels for updater execution.
var (
	EndpointUpdate      = "https://api.github.com/repos/underhax/go-webtoons-downloader/releases/latest"
	ErrResponseTooLarge = errors.New("response body exceeds size limit")
)

const (
	osWindows                 = "windows"
	maxMetadataResponseBytes  = 10 << 20
	maxAssetResponseBytes     = 64 << 20
	maxDecompressedAssetBytes = 128 << 20
)

func defaultNewGzipReader(r io.Reader) (io.ReadCloser, error) {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("gzip init error: %w", err)
	}
	return gzr, nil
}

func defaultZipOpen(f *zip.File) (io.ReadCloser, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("zip open error: %w", err)
	}
	return rc, nil
}

func defaultTempFileClose(f *os.File) error {
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	return nil
}

func defaultIOCopy(dst io.Writer, src io.Reader) (int64, error) {
	n, err := io.Copy(dst, src)
	if err != nil {
		return n, fmt.Errorf("io copy: %w", err)
	}
	return n, nil
}

func defaultNewRequestWithContext(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	return req, nil
}

// SupportedPlatforms maps OS/Arch combinations to asset filenames.
var SupportedPlatforms = map[string]string{
	"linux/amd64":   "go-webtoons-downloader-linux-amd64.tar.gz",
	"linux/arm64":   "go-webtoons-downloader-linux-arm64.tar.gz",
	"darwin/amd64":  "go-webtoons-downloader-macos-intel.tar.gz",
	"darwin/arm64":  "go-webtoons-downloader-macos-apple-silicon.tar.gz",
	"windows/amd64": "go-webtoons-downloader-windows-x64.zip",
	"windows/arm64": "go-webtoons-downloader-windows-arm64.zip",
}

var (
	osExecutable          = os.Executable
	filepathEvalSymlinks  = filepath.EvalSymlinks
	osRemove              = os.Remove
	osRename              = os.Rename
	osChmod               = os.Chmod
	osCreateTemp          = os.CreateTemp
	osWriteFile           = os.WriteFile
	osGOOS                = runtime.GOOS
	newGzipReader         = defaultNewGzipReader
	zipOpen               = defaultZipOpen
	tempFileClose         = defaultTempFileClose
	ioCopy                = defaultIOCopy
	newRequestWithContext = defaultNewRequestWithContext
)

type releaseInfo struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Digest             string `json:"digest"`
	} `json:"assets"`
}

func fetchLatestRelease(ctx context.Context, client *http.Client) (*releaseInfo, error) {
	req, err := newRequestWithContext(ctx, http.MethodGet, EndpointUpdate, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error fetching release: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && !errors.Is(cerr, http.ErrBodyReadAfterClose) {
			return
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status from GitHub API: %d", resp.StatusCode)
	}

	bodyBytes, err := readBoundedBody(resp.Body, maxMetadataResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read release response: %w", err)
	}

	var release releaseInfo
	if decodeErr := json.Unmarshal(bodyBytes, &release); decodeErr != nil {
		return nil, fmt.Errorf("failed to parse GitHub API response: %w", decodeErr)
	}
	return &release, nil
}

// Update downloads and applies the latest update from GitHub Releases.
func Update(ctx context.Context, client *http.Client, currentVersion string) error {
	if currentVersion == "dev" {
		return errors.New("update not available for development builds")
	}

	platformKey := fmt.Sprintf("%s/%s", osGOOS, runtime.GOARCH)
	assetName, supported := SupportedPlatforms[platformKey]
	if !supported {
		return fmt.Errorf("unsupported platform: %s", platformKey)
	}

	release, err := fetchLatestRelease(ctx, client)
	if err != nil {
		return err
	}

	if release.TagName == currentVersion {
		fmt.Printf("Current version %s is up-to-date.\n", currentVersion)
		return nil
	}

	var downloadURL string
	var expectedDigest string
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			downloadURL = asset.BrowserDownloadURL
			expectedDigest = asset.Digest
			break
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("asset %s not found in latest release", assetName)
	}

	if expectedDigest == "" {
		return fmt.Errorf("checksum for %s not found via GitHub API", assetName)
	}

	bodyBytes, err := downloadAndVerifyAsset(ctx, client, downloadURL, expectedDigest)
	if err != nil {
		return err
	}

	execPath, err := osExecutable()
	if err != nil {
		return fmt.Errorf("failed to determine executable path: %w", err)
	}
	execPath, err = filepathEvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("failed to evaluate symlinks for executable: %w", err)
	}

	if osGOOS == osWindows {
		err = replaceWindows(bytes.NewReader(bodyBytes), execPath)
	} else {
		err = replacePOSIX(bytes.NewReader(bodyBytes), execPath)
	}

	if err == nil {
		fmt.Printf("Successfully updated to %s!\n", release.TagName)
	}
	return err
}

func downloadAndVerifyAsset(ctx context.Context, client *http.Client, url, expectedDigest string) ([]byte, error) {
	dlReq, err := newRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create download request: %w", err)
	}

	dlResp, err := client.Do(dlReq)
	if err != nil {
		return nil, fmt.Errorf("network error downloading asset: %w", err)
	}
	defer func() {
		if cerr := dlResp.Body.Close(); cerr != nil && !errors.Is(cerr, http.ErrBodyReadAfterClose) {
			return
		}
	}()

	if dlResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status downloading asset: %d", dlResp.StatusCode)
	}

	bodyBytes, err := readBoundedBody(dlResp.Body, maxAssetResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to read downloaded asset: %w", err)
	}

	hash := sha256.Sum256(bodyBytes)
	actualHash := hex.EncodeToString(hash[:])
	expectedHashStr, hasPrefix := strings.CutPrefix(expectedDigest, "sha256:")
	if !hasPrefix || actualHash != expectedHashStr {
		return nil, fmt.Errorf("downloaded file checksum mismatch (expected: %s, got: %s)", expectedHashStr, actualHash)
	}
	return bodyBytes, nil
}

func defaultReadBoundedBody(r io.Reader, maxBytes int64) ([]byte, error) {
	lr := io.LimitReader(r, maxBytes+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, fmt.Errorf("read body stream: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrResponseTooLarge
	}
	return data, nil
}

var readBoundedBody = defaultReadBoundedBody

func replacePOSIX(body io.Reader, execPath string) error {
	gzr, err := newGzipReader(body)
	if err != nil {
		return fmt.Errorf("failed to initialize gzip reader: %w", err)
	}
	defer func() {
		if cerr := gzr.Close(); cerr != nil {
			return
		}
	}()

	boundedGzr := io.LimitReader(gzr, maxDecompressedAssetBytes+1)
	tr := tar.NewReader(boundedGzr)
	for {
		hdr, tarErr := tr.Next()
		if errors.Is(tarErr, io.EOF) {
			break
		}
		if tarErr != nil {
			return fmt.Errorf("failed to read tar archive: %w", tarErr)
		}

		if hdr.Size > maxDecompressedAssetBytes {
			return ErrResponseTooLarge
		}

		baseName := filepath.Base(hdr.Name)
		if hdr.Typeflag != tar.TypeReg || baseName != "wtd" {
			continue
		}

		return performAtomicSwap(tr, execPath)
	}

	return errors.New("wtd executable not found in archive")
}

func replaceWindows(body io.Reader, execPath string) error {
	data, err := readBoundedBody(body, maxAssetResponseBytes)
	if err != nil {
		return fmt.Errorf("failed to read zip body: %w", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("failed to initialize zip reader: %w", err)
	}

	for _, file := range zr.File {
		if file.UncompressedSize64 > maxDecompressedAssetBytes {
			return ErrResponseTooLarge
		}

		baseName := filepath.Base(file.Name)
		if file.FileInfo().IsDir() || baseName != "wtd.exe" {
			continue
		}

		rc, zErr := zipOpen(file)
		if zErr != nil {
			return fmt.Errorf("failed to open executable in zip: %w", zErr)
		}

		replaceErr := performWindowsReplace(rc, execPath)
		if closeErr := rc.Close(); closeErr != nil && replaceErr == nil {
			replaceErr = fmt.Errorf("failed to close zip entry: %w", closeErr)
		}
		return replaceErr
	}

	return errors.New("wtd.exe not found in archive")
}

func performAtomicSwap(src io.Reader, execPath string) error {
	cleanPath := filepath.Clean(execPath)
	dir := filepath.Dir(cleanPath)
	tempFile, err := osCreateTemp(dir, ".wtd-new-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return errors.New("permission denied. Please run with elevated privileges (e.g., sudo wtd --update)")
		}
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	tempName := tempFile.Name()
	defer func() {
		if rerr := osRemove(tempName); rerr != nil && !os.IsNotExist(rerr) {
			return
		}
	}()

	lr := io.LimitReader(src, maxDecompressedAssetBytes+1)
	written, copyErr := ioCopy(tempFile, lr)
	if copyErr != nil {
		if cerr := tempFileClose(tempFile); cerr != nil {
			return errors.Join(fmt.Errorf("failed to write new binary: %w", copyErr), cerr)
		}
		return fmt.Errorf("failed to write new binary: %w", copyErr)
	}
	if cerr := tempFileClose(tempFile); cerr != nil {
		return fmt.Errorf("close temp file: %w", cerr)
	}

	if written > maxDecompressedAssetBytes {
		return ErrResponseTooLarge
	}

	if chmodErr := osChmod(tempName, 0o755); chmodErr != nil {
		return fmt.Errorf("failed to make new binary executable: %w", chmodErr)
	}

	if renameErr := osRename(tempName, cleanPath); renameErr != nil {
		if errors.Is(renameErr, os.ErrPermission) {
			return errors.New("permission denied replacing executable. Please run with elevated privileges (e.g., sudo webtoons-downloader --update)")
		}
		return fmt.Errorf("failed to replace current executable: %w", renameErr)
	}

	return nil
}

func performWindowsReplace(src io.Reader, execPath string) error {
	cleanPath := filepath.Clean(execPath)
	oldPath := cleanPath + ".old"

	if err := osRemove(oldPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove old binary: %w", err)
	}

	if renameErr := osRename(cleanPath, oldPath); renameErr != nil {
		if errors.Is(renameErr, os.ErrPermission) || strings.Contains(renameErr.Error(), "Access is denied") {
			return errors.New("permission denied. Please run with Administrator privileges")
		}
		return fmt.Errorf("failed to rename current executable: %w", renameErr)
	}

	data, err := readBoundedBody(src, maxDecompressedAssetBytes)
	if err != nil {
		if rErr := osRename(oldPath, cleanPath); rErr != nil {
			return errors.Join(fmt.Errorf("failed to read new binary from archive: %w", err), rErr)
		}
		return fmt.Errorf("failed to read new binary from archive: %w", err)
	}

	if writeErr := osWriteFile(cleanPath, data, 0o755); writeErr != nil {
		if rErr := osRename(oldPath, cleanPath); rErr != nil {
			return errors.Join(writeErr, rErr)
		}
		if errors.Is(writeErr, os.ErrPermission) {
			return errors.New("permission denied while writing new binary")
		}
		return fmt.Errorf("failed to write new binary: %w", writeErr)
	}

	return nil
}

// CleanupWindowsOldFiles removes any leftover .old binary files from previous updates on Windows.
func CleanupWindowsOldFiles() {
	if osGOOS != osWindows {
		return
	}
	execPath, err := osExecutable()
	if err != nil {
		return
	}
	execPath, err = filepathEvalSymlinks(execPath)
	if err != nil {
		return
	}
	if err = osRemove(filepath.Clean(execPath + ".old")); err != nil && !os.IsNotExist(err) {
		return
	}
}
