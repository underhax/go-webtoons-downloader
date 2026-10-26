package updater

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestReplacePOSIX(t *testing.T) {
	dummyContent := "content-posix"
	validArchive := createDummyTarGz(t, "wtd", dummyContent)
	invalidArchive := []byte("invalid-gzip-content")
	var b bytes.Buffer
	gw := gzip.NewWriter(&b)
	if _, err := gw.Write([]byte("not a tar")); err != nil {
		t.Fatalf("failed to write dummy content: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
	validGzNoTar := b.Bytes()
	emptyArchive := createDummyTarGz(t, "not-wtd", dummyContent)

	origRename := osRename
	origRemove := osRemove
	origChmod := osChmod
	origCreateTemp := osCreateTemp
	origNewGzipReader := newGzipReader
	defer func() {
		osRename = origRename
		osRemove = origRemove
		osChmod = origChmod
		osCreateTemp = origCreateTemp
		newGzipReader = origNewGzipReader
	}()

	tests := []struct {
		mockCreate  func(string, string) (*os.File, error)
		mockChmod   func(string, os.FileMode) error
		mockRename  func(string, string) error
		body        io.Reader
		name        string
		errContains string
		wantErr     bool
	}{
		{
			name:        "invalid_gzip",
			body:        bytes.NewBuffer(invalidArchive),
			wantErr:     true,
			errContains: "failed to initialize gzip reader",
		},
		{
			name:        "missing_binary_posix",
			body:        bytes.NewBuffer(emptyArchive),
			wantErr:     true,
			errContains: "wtd executable not found in archive",
		},
		{
			name:        "tar_reading_error",
			body:        bytes.NewBuffer(validGzNoTar),
			wantErr:     true,
			errContains: "failed to read tar archive",
		},
		{
			name: "create_temp_permission_denied",
			body: bytes.NewBuffer(validArchive),
			mockCreate: func(_, _ string) (*os.File, error) {
				return nil, os.ErrPermission
			},
			wantErr:     true,
			errContains: "elevated",
		},
		{
			name: "create_temp_other_error",
			body: bytes.NewBuffer(validArchive),
			mockCreate: func(_, _ string) (*os.File, error) {
				return nil, errors.New("other error")
			},
			wantErr:     true,
			errContains: "failed to create temporary file",
		},
		{
			name: "chmod_error",
			body: bytes.NewBuffer(validArchive),
			mockChmod: func(_ string, _ os.FileMode) error {
				return errors.New("chmod fail")
			},
			wantErr:     true,
			errContains: "failed to make new binary executable",
		},
		{
			name: "rename_permission_denied",
			body: bytes.NewBuffer(validArchive),
			mockRename: func(_, _ string) error {
				return os.ErrPermission
			},
			wantErr:     true,
			errContains: "elevated",
		},
		{
			name: "rename_other_error",
			body: bytes.NewBuffer(validArchive),
			mockRename: func(_, _ string) error {
				return errors.New("rename fail")
			},
			wantErr:     true,
			errContains: "failed to replace current executable",
		},
		{
			name:    "success_posix",
			body:    bytes.NewBuffer(validArchive),
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			execPath := tempDir + "/webtoons-downloader"
			if err := os.WriteFile(execPath, []byte("old-binary"), 0o600); err != nil {
				t.Fatalf("failed to write dummy exec: %v", err)
			}

			if tc.mockCreate != nil {
				osCreateTemp = tc.mockCreate
			} else {
				osCreateTemp = os.CreateTemp
			}
			if tc.mockChmod != nil {
				osChmod = tc.mockChmod
			} else {
				osChmod = os.Chmod
			}
			if tc.mockRename != nil {
				osRename = tc.mockRename
			} else {
				osRename = os.Rename
			}

			err := replacePOSIX(tc.body, execPath)
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
