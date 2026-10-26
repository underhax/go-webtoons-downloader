package writer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/underhax/go-webtoons-downloader/internal/writer"
)

type dummyWriter struct{}

func (d *dummyWriter) WriteChapter(_ context.Context, _ string, _ []writer.Page, _ *writer.ChapterMetadata) error {
	return nil
}

func (d *dummyWriter) Extension() string {
	return ".dummy"
}

func TestRegistry(t *testing.T) {
	t.Parallel()

	writer.Register("mock_fmt", func() writer.ChapterWriter {
		return &dummyWriter{}
	})

	tests := []struct {
		expectErr error
		name      string
		format    string
		expectExt string
	}{
		{
			expectErr: nil,
			name:      "registered format creates successfully",
			format:    "mock_fmt",
			expectExt: ".dummy",
		},
		{
			expectErr: writer.ErrUnknownFormat,
			name:      "unregistered format returns error",
			format:    "unregistered_format",
			expectExt: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w, err := writer.Create(tc.format)
			if tc.expectErr != nil {
				if !errors.Is(err, tc.expectErr) {
					t.Fatalf("expected error %v, got %v", tc.expectErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if w.Extension() != tc.expectExt {
				t.Errorf("expected extension %s, got %s", tc.expectExt, w.Extension())
			}
		})
	}
}
