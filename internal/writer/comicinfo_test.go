package writer

import (
	"errors"
	"strings"
	"testing"
)

func mockFailingXMLMarshal(_ any, _, _ string) ([]byte, error) {
	return nil, errors.New("simulated xml marshal failure")
}

func TestBuildComicInfoXML(t *testing.T) {
	t.Parallel()

	t.Run("nil metadata returns nil bytes without error", func(t *testing.T) {
		t.Parallel()
		data, err := BuildComicInfoXML(nil)
		if err != nil {
			t.Fatalf("unexpected error for nil metadata: %v", err)
		}
		if data != nil {
			t.Errorf("expected nil data, got: %s", string(data))
		}
	})

	t.Run("valid metadata with chapter number", func(t *testing.T) {
		t.Parallel()
		meta := &ChapterMetadata{
			SeriesTitle:   "SeriesOne",
			ChapterTitle:  "ChapterOne",
			Author:        "AuthorOne",
			Genre:         "Action",
			Summary:       "Summary text",
			WebURL:        "https://example.com/ch1",
			ChapterNumber: 12,
		}
		data, err := BuildComicInfoXML(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		xmlStr := string(data)
		if !strings.Contains(xmlStr, "<Series>SeriesOne</Series>") {
			t.Errorf("expected Series tag, got: %s", xmlStr)
		}
		if !strings.Contains(xmlStr, "<Number>12</Number>") {
			t.Errorf("expected Number tag, got: %s", xmlStr)
		}
	})

	t.Run("valid metadata without chapter number", func(t *testing.T) {
		t.Parallel()
		meta := &ChapterMetadata{
			SeriesTitle:  "SeriesZero",
			ChapterTitle: "Prologue",
		}
		data, err := BuildComicInfoXML(meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		xmlStr := string(data)
		if strings.Contains(xmlStr, "<Number>") {
			t.Errorf("expected no Number tag for chapter 0, got: %s", xmlStr)
		}
	})
}

func TestBuildComicInfoXMLMarshalError(t *testing.T) {
	origMarshal := xmlMarshalIndent
	defer func() {
		xmlMarshalIndent = origMarshal
	}()

	xmlMarshalIndent = mockFailingXMLMarshal
	meta := &ChapterMetadata{SeriesTitle: "FailSeries"}
	_, err := BuildComicInfoXML(meta)
	if err == nil || !strings.Contains(err.Error(), "marshal comicinfo xml") {
		t.Errorf("expected marshal error, got: %v", err)
	}
}
