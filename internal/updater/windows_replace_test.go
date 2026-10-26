package updater

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestReplaceWindows(t *testing.T) {
	dummyContent := "content-win"
	validArchive := createDummyZip(t, "wtd.exe", dummyContent)
	emptyArchive := createDummyZip(t, "not-wtd.exe", dummyContent)

	origRename := osRename
	origRemove := osRemove
	origWrite := osWriteFile
	origZipOpen := zipOpen
	defer func() {
		osRename = origRename
		osRemove = origRemove
		osWriteFile = origWrite
		zipOpen = origZipOpen
	}()

	tests := []struct {
		mockRename  func(string, string) error
		mockWrite   func(string, []byte, os.FileMode) error
		mockZipOpen func(*zip.File) (io.ReadCloser, error)
		body        io.Reader
		name        string
		errContains string
		wantErr     bool
	}{
		{
			name:        "read_body_error",
			body:        errReader{},
			wantErr:     true,
			errContains: "failed to read zip body",
		},
		{
			name:        "invalid_zip",
			body:        bytes.NewBufferString("invalid-zip"),
			wantErr:     true,
			errContains: "failed to initialize zip reader",
		},
		{
			name:        "oversized_zip_entry",
			body:        bytes.NewBuffer(createDummyZipWithUncompressedSize(t, "wtd.exe", dummyContent, maxDecompressedAssetBytes+1)),
			wantErr:     true,
			errContains: "response body exceeds size limit",
		},
		{
			name:        "missing_binary_win",
			body:        bytes.NewBuffer(emptyArchive),
			wantErr:     true,
			errContains: "wtd.exe not found in archive",
		},
		{
			name:        "directory_entry_skipped_win",
			body:        bytes.NewBuffer(createDummyZip(t, "wtd.exe/", "")),
			wantErr:     true,
			errContains: "wtd.exe not found in archive",
		},
		{
			name: "rename_permission_denied_win",
			body: bytes.NewBuffer(validArchive),
			mockRename: func(_, _ string) error {
				return os.ErrPermission
			},
			wantErr:     true,
			errContains: "permission denied. Please run with Administrator privileges",
		},
		{
			name: "rename_other_error_win",
			body: bytes.NewBuffer(validArchive),
			mockRename: func(_, _ string) error {
				return errors.New("rename err")
			},
			wantErr:     true,
			errContains: "failed to rename current executable",
		},
		{
			name:       "write_permission_denied",
			body:       bytes.NewBuffer(validArchive),
			mockRename: func(_, _ string) error { return nil },
			mockWrite: func(_ string, _ []byte, _ os.FileMode) error {
				return os.ErrPermission
			},
			wantErr:     true,
			errContains: "permission denied while writing new binary",
		},
		{
			name:       "write_other_error",
			body:       bytes.NewBuffer(validArchive),
			mockRename: func(_, _ string) error { return nil },
			mockWrite: func(_ string, _ []byte, _ os.FileMode) error {
				return errors.New("write err")
			},
			wantErr:     true,
			errContains: "failed to write new binary",
		},
		{
			name:       "success_win",
			body:       bytes.NewBuffer(validArchive),
			mockRename: func(_, _ string) error { return nil },
			mockWrite:  func(_ string, _ []byte, _ os.FileMode) error { return nil },
			wantErr:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			execPath := tempDir + "/webtoons-downloader.exe"
			if err := os.WriteFile(execPath, []byte("old-binary-win"), 0o600); err != nil {
				t.Fatalf("failed to write dummy exec: %v", err)
			}

			if tc.mockRename != nil {
				osRename = tc.mockRename
			} else {
				osRename = os.Rename
			}
			if tc.mockWrite != nil {
				osWriteFile = tc.mockWrite
			} else {
				osWriteFile = os.WriteFile
			}
			if tc.mockZipOpen != nil {
				zipOpen = tc.mockZipOpen
			} else {
				zipOpen = defaultZipOpen
			}

			err := replaceWindows(tc.body, execPath)
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

func TestCleanupWindowsOldFiles(t *testing.T) {
	origGOOS := osGOOS
	defer func() { osGOOS = origGOOS }()

	osGOOS = "linux"
	CleanupWindowsOldFiles()

	osGOOS = osWindows
	origExecutable := osExecutable
	origEval := filepathEvalSymlinks
	origRemove := osRemove
	defer func() {
		osExecutable = origExecutable
		filepathEvalSymlinks = origEval
		osRemove = origRemove
	}()

	tempDir := t.TempDir()
	dummyExec := tempDir + "/app.exe"
	oldExec := dummyExec + ".old"
	if err := os.WriteFile(oldExec, []byte("old"), 0o600); err != nil {
		t.Fatalf("failed to create old file: %v", err)
	}

	osExecutable = func() (string, error) { return dummyExec, nil }
	filepathEvalSymlinks = func(p string) (string, error) { return p, nil }

	CleanupWindowsOldFiles()

	if _, err := os.Stat(oldExec); !os.IsNotExist(err) {
		t.Errorf("expected %s to be removed", oldExec)
	}
}
