package writer

import (
	"errors"
	"fmt"
)

// ErrUnknownFormat indicates that no factory is registered for the specified format.
var ErrUnknownFormat = errors.New("unknown chapter writer format")

// FactoryFunc constructs an instance of a ChapterWriter.
type FactoryFunc func() ChapterWriter

var registry = make(map[string]FactoryFunc)

// Register associates a format name with its corresponding ChapterWriter factory.
func Register(format string, fn FactoryFunc) {
	registry[format] = fn
}

// Create builds a new ChapterWriter for the requested format.
func Create(format string) (ChapterWriter, error) {
	fn, exists := registry[format]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnknownFormat, format)
	}
	return fn(), nil
}
