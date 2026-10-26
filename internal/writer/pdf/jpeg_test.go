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

func TestValidateImageDimensions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		errExpected error
		name        string
		width       int
		height      int
	}{
		{
			name:        "zero width",
			width:       0,
			height:      100,
			errExpected: ErrInvalidImage,
		},
		{
			name:        "zero height",
			width:       100,
			height:      0,
			errExpected: ErrInvalidImage,
		},
		{
			name:        "negative width",
			width:       -1,
			height:      100,
			errExpected: ErrInvalidImage,
		},
		{
			name:        "negative height",
			width:       100,
			height:      -1,
			errExpected: ErrInvalidImage,
		},
		{
			name:        "width exceeds maxImageDimension",
			width:       maxImageDimension + 1,
			height:      100,
			errExpected: ErrInvalidImage,
		},
		{
			name:        "height exceeds maxImageDimension",
			width:       100,
			height:      maxImageDimension + 1,
			errExpected: ErrInvalidImage,
		},
		{
			name:        "pixel count exceeds maxImagePixels",
			width:       10000,
			height:      10000,
			errExpected: ErrInvalidImage,
		},
		{
			name:        "valid dimensions",
			width:       800,
			height:      1200,
			errExpected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateImageDimensions(tc.width, tc.height)
			if tc.errExpected != nil {
				if !errors.Is(err, tc.errExpected) {
					t.Fatalf("expected error %v, got %v", tc.errExpected, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestReadSOFDimensionsDefensive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		errExpected   error
		name          string
		data          []byte
		offset        int
		segmentLength int
		expW          int
		expH          int
	}{
		{
			name:          "segment length less than 7",
			data:          []byte{0x00, 0x05, 0x08, 0x01, 0x00, 0x01, 0x00},
			offset:        0,
			segmentLength: 5,
			errExpected:   ErrInvalidImage,
		},
		{
			name:          "offset plus 7 exceeds buffer",
			data:          []byte{0x00, 0x08, 0x08, 0x01},
			offset:        0,
			segmentLength: 8,
			errExpected:   ErrInvalidImage,
		},
		{
			name: "oversized dimensions return error",
			data: []byte{
				0x00, 0x09, 0x08,
				0xFF, 0xFF,
				0xFF, 0xFF,
			},
			offset:        0,
			segmentLength: 9,
			errExpected:   ErrInvalidImage,
			expH:          65535,
			expW:          65535,
		},
		{
			name: "valid dimensions return nil",
			data: []byte{
				0x00, 0x09, 0x08,
				0x01, 0x00,
				0x02, 0x00,
			},
			offset:        0,
			segmentLength: 9,
			errExpected:   nil,
			expH:          256,
			expW:          512,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w, h, err := readSOFDimensions(tc.data, tc.offset, tc.segmentLength)
			if tc.errExpected != nil {
				if !errors.Is(err, tc.errExpected) {
					t.Fatalf("expected error %v, got %v", tc.errExpected, err)
				}
				if tc.expW != 0 && (w != tc.expW || h != tc.expH) {
					t.Errorf("expected %dx%d, got %dx%d", tc.expW, tc.expH, w, h)
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
