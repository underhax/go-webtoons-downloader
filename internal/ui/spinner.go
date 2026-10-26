// Package ui provides console progress rendering including interactive spinners.
package ui

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

var defaultFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner encapsulates terminal animation and progress rendering state.
type Spinner struct {
	start      time.Time
	out        io.Writer
	timeSource func() time.Time
	cancel     context.CancelFunc
	done       chan struct{}
	completed  *atomic.Int32
	label      string
	frames     []string
	interval   time.Duration
	total      int
	mu         sync.RWMutex
}

// Option configures custom spinner options.
type Option func(*Spinner)

// WithInterval sets a custom animation refresh rate.
func WithInterval(d time.Duration) Option {
	return func(s *Spinner) {
		s.interval = d
	}
}

// WithTimeSource enables deterministic time testing.
func WithTimeSource(f func() time.Time) Option {
	return func(s *Spinner) {
		s.timeSource = f
	}
}

// Start initiates the non-blocking UI update loop for progress reporting.
func Start(ctx context.Context, label string, completed *atomic.Int32, total int, out io.Writer, opts ...Option) *Spinner {
	if out == nil || out == io.Discard {
		return nil
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	s := &Spinner{
		timeSource: time.Now,
		out:        out,
		completed:  completed,
		label:      label,
		frames:     defaultFrames,
		interval:   150 * time.Millisecond,
		total:      total,
		cancel:     cancel,
		done:       make(chan struct{}),
	}

	for _, opt := range opts {
		opt(s)
	}

	s.start = s.timeSource()

	go func() {
		defer close(s.done)
		s.run(cancelCtx)
	}()

	return s
}

// Stop terminates the spinner animation and clears the terminal line.
func (s *Spinner) Stop() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
	<-s.done
}

// Update dynamically refreshes the displayed label, total item count, and completed counter.
func (s *Spinner) Update(label string, total int, completed *atomic.Int32) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if label != s.label {
		s.label = label
		s.start = s.timeSource()
	}
	s.total = total
	s.completed = completed
}

func (s *Spinner) run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	i := 0

	for {
		select {
		case <-ctx.Done():
			if _, err := fmt.Fprint(s.out, "\r\033[K"); err != nil {
				return
			}
			return
		case <-ticker.C:
			s.printFrame(i)
			i++
		}
	}
}

func (s *Spinner) printFrame(i int) {
	s.mu.RLock()
	c := int32(0)
	if s.completed != nil {
		c = s.completed.Load()
	}
	elapsed := s.timeSource().Sub(s.start).Round(time.Second)
	frame := s.frames[i%len(s.frames)]
	label := s.label
	total := s.total
	out := s.out
	s.mu.RUnlock()

	if total <= 0 {
		if _, err := fmt.Fprintf(out, "\r\033[K%s %s (Elapsed: %s)", frame, label, elapsed); err != nil {
			return
		}
	} else {
		if _, err := fmt.Fprintf(out, "\r\033[K%s %s [%d/%d] (Elapsed: %s)", frame, label, c, total, elapsed); err != nil {
			return
		}
	}
}
