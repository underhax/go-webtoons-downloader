package pdf

import (
	"errors"
	"testing"
)

func TestParseJPEGDimensionsBranches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		errExpected error
		name        string
		data        []byte
		expW        int
		expH        int
	}{
		{
			errExpected: ErrInvalidImage,
			name:        "too short slice",
			data:        []byte{0xFF, 0xD8, 0xFF},
		},
		{
			errExpected: ErrInvalidImage,
			name:        "missing marker prefix",
			data:        []byte{0xFF, 0xD8, 0x01, 0x02, 0x03},
		},
		{
			errExpected: ErrInvalidImage,
			name:        "offset exceeds length after prefix",
			data:        []byte{0xFF, 0xD8, 0xFF, 0xFF},
		},
		{
			errExpected: ErrInvalidImage,
			name:        "segment length header truncated",
			data:        []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00},
		},
		{
			errExpected: ErrInvalidImage,
			name:        "segment length less than two",
			data:        []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x01},
		},
		{
			errExpected: ErrInvalidImage,
			name:        "segment length exceeds buffer length",
			data:        []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x20, 0x00},
		},
		{
			errExpected: ErrInvalidImage,
			name:        "sof segment length too short",
			data:        []byte{0xFF, 0xD8, 0xFF, 0xC0, 0x00, 0x04, 0x00, 0x00},
		},
		{
			errExpected: nil,
			name:        "valid sof segment dimensions parsed",
			data: []byte{
				0xFF, 0xD8,
				0xFF, 0x00,
				0xFF, 0xC0, 0x00, 0x09, 0x08, 0x01, 0x2C, 0x01, 0x90, 0x03, 0x01,
			},
			expH: 300,
			expW: 400,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w, h, err := parseJPEGDimensions(tc.data)
			if tc.errExpected != nil {
				if !errors.Is(err, tc.errExpected) {
					t.Fatalf("expected error %v, got %v", tc.errExpected, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if w != tc.expW || h != tc.expH {
				t.Errorf("expected %dx%d, got %dx%d", tc.expW, tc.expH, w, h)
			}
		})
	}
}

func TestIsSOFMarkerTable(t *testing.T) {
	t.Parallel()

	validMarkers := []byte{
		0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF,
	}

	for _, marker := range validMarkers {
		if !isSOFMarker(marker) {
			t.Errorf("expected marker 0x%X to be valid SOF", marker)
		}
	}

	invalidMarkers := []byte{0x00, 0xD8, 0xD9, 0xDA, 0xC4, 0xE0, 0xFE}
	for _, marker := range invalidMarkers {
		if isSOFMarker(marker) {
			t.Errorf("expected marker 0x%X to be non-SOF", marker)
		}
	}
}
