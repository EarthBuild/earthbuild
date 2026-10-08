// Package writersub implements logbus subscribers that format and write log streams to standard output or files.
package writersub

import (
	"errors"
	"io"
	"sync"

	"github.com/EarthBuild/earthbuild/logstream"
)

// WriterSub is a bus subscriber that can print formatted logs to a writer.
type WriterSub struct {
	w              io.Writer
	err            error
	targetIDFilter string
	mu             sync.Mutex
}

// New creates a new WriterSub.
func New(w io.Writer, targetIDFilter string) *WriterSub {
	return &WriterSub{
		w:              w,
		targetIDFilter: targetIDFilter,
	}
}

// Write writes the given delta to the writer, if it is a formatted log delta.
func (ws *WriterSub) Write(delta *logstream.Delta) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	switch d := delta.GetDeltaTypeOneof().(type) {
	case *logstream.Delta_DeltaFormattedLog:
		if ws.targetIDFilter != "" && d.DeltaFormattedLog.GetTargetId() != ws.targetIDFilter {
			return
		}

		_, err := ws.w.Write(d.DeltaFormattedLog.GetData())
		if err != nil {
			ws.err = errors.Join(ws.err, err)
			return
		}
	default:
	}
}

// Err returns any error that occurred while writing to the writer.
func (ws *WriterSub) Err() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	return ws.err
}
