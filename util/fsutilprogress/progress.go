package fsutilprogress

import (
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/dustin/go-humanize"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
)

// ProgressCallback exposes callbacks for displaying status on files being sent
// or received. It is implemented entirely on top of stock fsutil hooks (no fork).
type ProgressCallback interface {
	// Info is the coarse aggregate progress callback (fsutil Send/Receive ProgressCb).
	Info(numBytes int, last bool)
	// OnReceiveFile is an fsutil.ChangeFunc for ReceiveOpt.NotifyHashed: one call per received file.
	OnReceiveFile(kind fsutil.ChangeKind, relPath string, fi os.FileInfo, err error) error
	// WrapMap decorates an fsutil FilterOpt.Map func to report per-file send activity.
	WrapMap(inner func(string, *fstypes.Stat) fsutil.MapResult) func(string, *fstypes.Stat) fsutil.MapResult
}

type progressCallback struct {
	lastUpdate        time.Time
	pathPrefix        string
	console           conslogging.ConsoleLogger
	numStats          int
	numSent           int
	numReceived       int
	bytesSent         int
	bytesReceived     int
	lastBytesSent     int
	lastBytesReceived int
	mutex             sync.Mutex
}

// New returns a new progress callback for use with fsutil.
func New(pathPrefix string, console conslogging.ConsoleLogger) ProgressCallback {
	return &progressCallback{
		console:    console,
		pathPrefix: pathPrefix,
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

// OnReceiveFile reports each file as it is received (fsutil NotifyHashed).
func (s *progressCallback) OnReceiveFile(kind fsutil.ChangeKind, relPath string, fi os.FileInfo, err error) error {
	if err != nil || kind == fsutil.ChangeKindDelete {
		return nil
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()

	var n int
	if fi != nil && !fi.IsDir() {
		n = int(fi.Size())
	}
	s.bytesReceived += n
	s.numReceived++
	s.console.VerbosePrintf("received data for %s (%s)\n", path.Join(s.pathPrefix, relPath), humanizeBytes(n))
	s.displaySummaryLocked()
	return nil
}

// WrapMap reports each file as it is walked for sending. Stock fsutil has no
// per-file send-progress hook, so we observe via FilterOpt.Map and delegate to
// the wrapped map func.
func (s *progressCallback) WrapMap(inner func(string, *fstypes.Stat) fsutil.MapResult) func(string, *fstypes.Stat) fsutil.MapResult {
	return func(p string, st *fstypes.Stat) fsutil.MapResult {
		s.mutex.Lock()
		s.numStats++
		s.numSent++
		if st != nil {
			s.bytesSent += int(st.Size)
		}
		s.console.VerbosePrintf("sending %s\n", path.Join(s.pathPrefix, p))
		s.displaySummaryLocked()
		s.mutex.Unlock()
		if inner != nil {
			return inner(p, st)
		}
		return fsutil.MapResultKeep
	}
}

// displaySummaryLocked prints a periodic transfer summary; caller must hold s.mutex.
func (s *progressCallback) displaySummaryLocked() {
	now := time.Now()

	d := now.Sub(s.lastUpdate)
	if d <= time.Second*15 {
		return
	}

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
			"received %s (%s)%s\n", humanizeBytes(s.bytesReceived), puralize(s.numReceived, "file"), transferRate,
		)
	}

	s.lastUpdate = now
	s.lastBytesSent = s.bytesSent
	s.lastBytesReceived = s.bytesReceived
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
