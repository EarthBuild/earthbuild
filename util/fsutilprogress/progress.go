package fsutilprogress

import (
	"fmt"
	"path"
	"sync"
	"time"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/dustin/go-humanize"
	"github.com/tonistiigi/fsutil"
)

// ProgressCallback exposes two different levels of callbacks for displaying status on files being sent or received.
type ProgressCallback interface {
	Info(numBytes int, last bool)
	Verbose(relPath string, status fsutil.VerboseProgressStatus, numBytes int)
}

type progressCallback struct {
	console           conslogging.ConsoleLogger
	mutex             sync.Mutex
	pathPrefix        string
	numStats          int
	numSent           int
	numReceived       int
	bytesSent         int
	bytesReceived     int
	filesize          map[string]int
	lastUpdate        time.Time
	lastBytesSent     int
	lastBytesReceived int
}

// New returns a new verbose progress callback for use with fsutil.
func New(pathPrefix string, console conslogging.ConsoleLogger) ProgressCallback {
	return &progressCallback{
		console:    console,
		pathPrefix: pathPrefix,
		filesize:   map[string]int{},
	}
}

func (s *progressCallback) Info(numBytes int, last bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if last {
		format := "transferred %d file(s) for context %s (%s, %d file/dir stats)"
		s.console.Printf(format, s.numSent, s.pathPrefix, humanizeBytes(numBytes), s.numStats)
	}
}

func (s *progressCallback) Verbose(relPath string, status fsutil.VerboseProgressStatus, numBytes int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	fullPath := path.Join(s.pathPrefix, relPath)

	switch status {
	case fsutil.StatusStat:
		s.numStats++
		s.console.DebugPrintf("sent file stat for %s\n", fullPath)
	case fsutil.StatusSent:
		s.console.VerbosePrintf("sent data for %s (%s)\n", fullPath, humanizeBytes(numBytes))
		s.numSent++
		s.bytesSent += numBytes
	case fsutil.StatusReceiving:
		s.filesize[fullPath] += numBytes
		s.bytesReceived += numBytes
	case fsutil.StatusReceived:
		if numBytes == 0 {
			numBytes = s.filesize[fullPath]
		}

		s.console.VerbosePrintf("received data for %s (%s)\n", fullPath, humanizeBytes(numBytes))
		s.numReceived++
	case fsutil.StatusFailed:
		s.console.VerbosePrintf("sent data for %s failed\n", fullPath)
	case fsutil.StatusSkipped:
		s.console.VerbosePrintf("ignoring %s\n", fullPath)
	default:
		s.console.Warnf("unhandled progress status %v (path=%s, numBytes=%d)\n", status, fullPath, numBytes)
	}

	// display a summary every 15 seconds
	now := time.Now()

	d := now.Sub(s.lastUpdate)
	if d > time.Second*15 {
		if s.numSent > 0 {
			var transferRate string

			if !s.lastUpdate.IsZero() {
				bytes := humanize.Bytes(uint64(float64(s.bytesSent-s.lastBytesSent) / d.Seconds()))
				transferRate = fmt.Sprintf("; transfer rate: %s/s", bytes)
			}

			s.console.Printf("sent %s (%s)%s\n", humanizeBytes(s.bytesSent), puralize(s.numSent, "file"), transferRate)
		} else {
			s.console.Printf("sent %s\n", puralize(s.numStats, "file stat"))
		}

		if s.numReceived > 0 {
			var transferRate string

			if !s.lastUpdate.IsZero() {
				bytes := humanizeBytes(int(float64(s.bytesReceived-s.lastBytesReceived) / d.Seconds()))
				transferRate = fmt.Sprintf("; transfer rate: %s/s", bytes)
			}

			s.console.Printf(
				"received %s (%s)%s\n", humanizeBytes(s.bytesReceived), puralize(s.numReceived, "file"), transferRate)
		}

		s.lastUpdate = now
		s.lastBytesSent = s.bytesSent
		s.lastBytesReceived = s.bytesReceived
	}
}

func puralize(n int, suffix string) string {
	if n == 1 {
		return "1 " + suffix
	}

	return fmt.Sprintf("%d %ss", n, suffix)
}

func humanizeBytes(v int) string {
	var bytes uint64

	if v > 0 {
		bytes = uint64(v)
	}

	return humanize.Bytes(bytes)
}
