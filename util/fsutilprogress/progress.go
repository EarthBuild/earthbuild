package fsutilprogress

import (
	"fmt"
	"path"
	"sync"
	"time"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/dustin/go-humanize"
)

// ProgressCallback exposes callback for displaying status on files being sent or received.
type ProgressCallback interface {
	Info(numBytes int, last bool)
	File(relPath string, size int64)
}

type progressCallback struct {
	lastUpdate time.Time
	log        *conslogging.ConsoleLogger
	pathPrefix string
	lastBytes  int
	mutex      sync.Mutex
}

// New returns a new progress callback for use with fsutil.
func New(pathPrefix string, log *conslogging.ConsoleLogger) ProgressCallback {
	return &progressCallback{
		log:        log,
		pathPrefix: pathPrefix,
	}
}

func (s *progressCallback) Info(numBytes int, last bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if last {
		if s.pathPrefix != "" {
			s.log.Printf("transferred %s for context %s\n", humanizeBytes(numBytes), s.pathPrefix)
		} else {
			s.log.Printf("transferred %s\n", humanizeBytes(numBytes))
		}

		s.lastBytes = 0
		s.lastUpdate = time.Time{}

		return
	}

	// display a summary every 15 seconds
	now := time.Now()

	d := now.Sub(s.lastUpdate)
	if d <= time.Second*15 {
		return
	}

	var transferRate string

	if !s.lastUpdate.IsZero() {
		bytes := humanize.Bytes(uint64(float64(numBytes-s.lastBytes) / d.Seconds()))
		transferRate = fmt.Sprintf("; transfer rate: %s/s", bytes)
	}

	s.log.Printf("transferred %s%s\n", humanizeBytes(numBytes), transferRate)

	s.lastUpdate = now
	s.lastBytes = numBytes
}

func (s *progressCallback) File(relPath string, size int64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	fullPath := path.Join(s.pathPrefix, relPath)

	if size >= 0 {
		s.log.VerbosePrintf("received data for %s (%s)\n", fullPath, humanize.Bytes(uint64(size)))
	} else {
		s.log.VerbosePrintf("received data for %s\n", fullPath)
	}
}

func humanizeBytes(v int) string {
	var bytes uint64

	if v > 0 {
		bytes = uint64(v)
	}

	return humanize.Bytes(bytes)
}
