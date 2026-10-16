// Package fsutilprogress implements progress callbacks and tracking for file system operations.
package fsutilprogress

import (
	"sync"
	"time"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/dustin/go-humanize"
)

// ProgressCallback tracks and logs byte-transfer status for fsutil streams.
// Each fsutil stream reports cumulative bytes for that stream; when shared across
// streams, ProgressCallback resets its baseline on stream completion or if a stream
// reports a lower cumulative byte count than the previous observation.
type ProgressCallback struct {
	lastUpdate  time.Time
	log         *conslogging.ConsoleLogger
	now         func() time.Time // injectable clock for testing; defaults to time.Now
	contextName string
	lastBytes   int
	mu          sync.Mutex
}

// New returns a progress callback that logs periodic transfer rate summaries.
func New(log *conslogging.ConsoleLogger) *ProgressCallback {
	return NewContext("", log)
}

// NewContext returns a progress callback that logs periodic transfer rate summaries
// and a completion message for the given build context.
func NewContext(contextName string, log *conslogging.ConsoleLogger) *ProgressCallback {
	return &ProgressCallback{
		log:         log,
		now:         time.Now,
		contextName: contextName,
	}
}

// Info logs periodic transfer rate summaries and completion status for transferred bytes.
func (p *ProgressCallback) Info(numBytes int, last bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	numBytes = max(0, numBytes)

	if last {
		if p.contextName != "" {
			p.log.Printf("transferred %s for context %s\n", humanizeBytes(numBytes), p.contextName)
		}

		p.lastBytes = 0
		p.lastUpdate = time.Time{}

		return
	}

	now := p.now()

	if p.lastUpdate.IsZero() || numBytes < p.lastBytes {
		p.lastUpdate = now
		p.lastBytes = numBytes

		return
	}

	// display a summary every 15 seconds
	d := now.Sub(p.lastUpdate)
	if d <= time.Second*15 {
		return
	}

	rate := humanizeBytes(float64(numBytes-p.lastBytes) / d.Seconds())
	p.log.Printf("transferred %s; transfer rate: %s/s\n", humanizeBytes(numBytes), rate)

	p.lastUpdate = now
	p.lastBytes = numBytes
}

// ReceiveFile logs a verbose message when an individual file is received.
func (p *ProgressCallback) ReceiveFile(relPath string, size int64) {
	if size >= 0 {
		p.log.VerbosePrintf("received data for %s (%s)\n", relPath, humanizeBytes(size))
		return
	}

	p.log.VerbosePrintf("received data for %s\n", relPath)
}

func humanizeBytes[T ~int | ~int64 | ~float64](v T) string {
	return humanize.Bytes(uint64(max(0, v)))
}
