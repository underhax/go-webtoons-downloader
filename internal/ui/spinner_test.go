package ui_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/underhax/go-webtoons-downloader/internal/ui"
)

func TestSpinnerLifecycle(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	completed := &atomic.Int32{}
	completed.Store(5)

	fixedTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	timeFunc := func() time.Time {
		return fixedTime
	}

	sp := ui.Start(
		context.Background(),
		"Downloading",
		completed,
		10,
		&buf,
		ui.WithInterval(10*time.Millisecond),
		ui.WithTimeSource(timeFunc),
	)

	time.Sleep(35 * time.Millisecond)
	completed.Store(8)
	sp.Update("Downloading", 12, completed)
	sp.Update("NewChapter", 20, completed)
	sp.Stop()

	out := buf.String()
	if out == "" {
		t.Errorf("expected spinner output in buffer, got empty")
	}
}

func TestSpinnerDiscardAndNil(t *testing.T) {
	t.Parallel()

	spNil := ui.Start(context.Background(), "test", nil, 0, nil)
	spNil.Stop()
	spNil.Update("nil-update", 0, nil)

	spDiscard := ui.Start(context.Background(), "test", nil, 0, io.Discard)
	spDiscard.Stop()
	spDiscard.Update("discard-update", 0, nil)
}

type errWriter struct{}

func (errWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write failure")
}

func TestSpinnerWithoutTotal(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	sp := ui.Start(
		context.Background(),
		"Processing",
		nil,
		0,
		&buf,
		ui.WithInterval(5*time.Millisecond),
	)

	time.Sleep(15 * time.Millisecond)
	sp.Stop()

	out := buf.String()
	if !strings.Contains(out, "Processing") {
		t.Errorf("expected output to contain Processing, got %q", out)
	}
}

func TestSpinnerFailingWriter(t *testing.T) {
	t.Parallel()

	spWithTotal := ui.Start(
		context.Background(),
		"FailedTotal",
		nil,
		10,
		errWriter{},
		ui.WithInterval(5*time.Millisecond),
	)
	time.Sleep(15 * time.Millisecond)
	spWithTotal.Stop()

	spNoTotal := ui.Start(
		context.Background(),
		"FailedNoTotal",
		nil,
		0,
		errWriter{},
		ui.WithInterval(5*time.Millisecond),
	)
	time.Sleep(15 * time.Millisecond)
	spNoTotal.Stop()
}

func TestSpinnerNilReceivers(t *testing.T) {
	t.Parallel()

	var sp *ui.Spinner
	sp.Stop()
	sp.Update("test", 5, nil)
}
