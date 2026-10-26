// Package pdf implements pure Go PDF generation for comic chapters.
package pdf

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
)

// ErrInvalidImage indicates that the image payload could not be parsed or dimensioned.
var ErrInvalidImage = errors.New("invalid image payload")

func defaultJPEGEncode(w io.Writer, m image.Image, o *jpeg.Options) error {
	if err := jpeg.Encode(w, m, o); err != nil {
		return fmt.Errorf("encode jpeg payload: %w", err)
	}
	return nil
}

var jpegEncode = defaultJPEGEncode

type imageMetadata struct {
	data   []byte
	width  int
	height int
}

func extractOrNormalizeImage(input []byte) (*imageMetadata, error) {
	if isJPEG(input) {
		w, h, err := parseJPEGDimensions(input)
		if err == nil && w > 0 && h > 0 {
			return &imageMetadata{
				data:   input,
				width:  w,
				height: h,
			}, nil
		}
	}

	var decodedImg image.Image
	var err error
	decodedImg, err = jpeg.Decode(bytes.NewReader(input))
	if err != nil {
		var pngErr error
		decodedImg, pngErr = png.Decode(bytes.NewReader(input))
		if pngErr != nil {
			return nil, fmt.Errorf("%w: decode image raster: %w", ErrInvalidImage, errors.Join(err, pngErr))
		}
	}

	bounds := decodedImg.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	var buf bytes.Buffer
	if err := jpegEncode(&buf, decodedImg, &jpeg.Options{Quality: 95}); err != nil {
		return nil, fmt.Errorf("%w: encode jpeg: %w", ErrInvalidImage, err)
	}

	return &imageMetadata{
		data:   buf.Bytes(),
		width:  width,
		height: height,
	}, nil
}

func isJPEG(data []byte) bool {
	return len(data) >= 2 && data[0] == 0xFF && data[1] == 0xD8
}

func parseJPEGDimensions(data []byte) (width, height int, err error) {
	if len(data) < 4 {
		return 0, 0, ErrInvalidImage
	}

	offset := 2
	dataLen := len(data)

	for offset < dataLen-1 {
		if data[offset] != 0xFF {
			return 0, 0, ErrInvalidImage
		}

		for offset < dataLen && data[offset] == 0xFF {
			offset++
		}

		if offset >= dataLen {
			break
		}

		marker := data[offset]
		offset++

		if marker == 0xD8 || marker == 0xD9 || marker == 0x00 {
			continue
		}

		if offset+2 > dataLen {
			break
		}

		segmentLength := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if segmentLength < 2 || offset+segmentLength > dataLen {
			break
		}

		if isSOFMarker(marker) {
			return readSOFDimensions(data, offset, segmentLength)
		}

		offset += segmentLength
	}

	return 0, 0, ErrInvalidImage
}

func readSOFDimensions(data []byte, offset, segmentLength int) (width, height int, err error) {
	if segmentLength < 7 || offset+7 > len(data) {
		return 0, 0, ErrInvalidImage
	}

	height = int(binary.BigEndian.Uint16(data[offset+3 : offset+5]))
	width = int(binary.BigEndian.Uint16(data[offset+5 : offset+7]))
	return width, height, nil
}

func isSOFMarker(marker byte) bool {
	switch marker {
	case 0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF:
		return true
	default:
		return false
	}
}
