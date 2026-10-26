package raw

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/underhax/go-webtoons-downloader/internal/writer"
)

func mockFailingBuildComicInfo(_ *writer.ChapterMetadata) ([]byte, error) {
	return nil, errors.New("simulated build comicinfo failure")
}

func mockFailingWriteComicInfoFile(_ string, _ *writer.ChapterMetadata) error {
	return errors.New("simulated write comicinfo file failure")
}

func mockFailingOSWriteFile(_ string, _ []byte, _ os.FileMode) error {
	return errors.New("simulated os write file failure")
}

func mockFailingMkdirAll(_ string, _ os.FileMode) error {
	return errors.New("simulated mkdir failure")
}

func TestRawWriterComicInfoErrors(t *testing.T) {
	meta := &writer.ChapterMetadata{SeriesTitle: "RawSeries"}

	t.Run("writeComicInfoFileFunc failure in WriteChapter", func(t *testing.T) {
		origFunc := writeComicInfoFileFunc
		defer func() { writeComicInfoFileFunc = origFunc }()
		writeComicInfoFileFunc = mockFailingWriteComicInfoFile

		w := New()
		err := w.WriteChapter(context.Background(), t.TempDir(), []writer.Page{{Data: []byte("img"), Index: 1}}, meta)
		if err == nil || !strings.Contains(err.Error(), "simulated write comicinfo file failure") {
			t.Errorf("expected write comicinfo error, got: %v", err)
		}
	})

	t.Run("defaultWriteComicInfoFile build failure", func(t *testing.T) {
		origBuild := buildComicInfoFunc
		defer func() { buildComicInfoFunc = origBuild }()
		buildComicInfoFunc = mockFailingBuildComicInfo

		err := defaultWriteComicInfoFile(t.TempDir(), meta)
		if err == nil || !strings.Contains(err.Error(), "marshal comicinfo xml") {
			t.Errorf("expected marshal error, got: %v", err)
		}
	})

	t.Run("defaultWriteComicInfoFile os write failure", func(t *testing.T) {
		origWrite := osWriteFile
		defer func() { osWriteFile = origWrite }()
		osWriteFile = mockFailingOSWriteFile

		err := defaultWriteComicInfoFile(t.TempDir(), meta)
		if err == nil || !strings.Contains(err.Error(), "write comicinfo file") {
			t.Errorf("expected os write file error, got: %v", err)
		}
	})

	t.Run("osMkdirAll failure in WriteChapter", func(t *testing.T) {
		origMkdir := osMkdirAll
		defer func() { osMkdirAll = origMkdir }()
		osMkdirAll = mockFailingMkdirAll

		w := New()
		err := w.WriteChapter(context.Background(), t.TempDir(), []writer.Page{{Data: []byte("img"), Index: 1}}, meta)
		if err == nil || !strings.Contains(err.Error(), "create raw chapter directory") {
			t.Errorf("expected create raw chapter directory error, got: %v", err)
		}
	})
}
