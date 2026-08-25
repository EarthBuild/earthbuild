//go:build linux

package trace

import (
	"fmt"
	"os"
	"strconv"
)

// memFiles keeps `/proc/<pid>/mem` open for one process at a time.
//
// **Opening it is 4.25µs where the whole handler is 6.9µs** (E681): a traced
// call reads the path out of the stopped process, and it was opening and
// closing the same file it had opened for the call before.
//
// One process rather than a map of them. A step forks thousands, a cache with an
// entry each holds a descriptor each, and this engine has already overflowed a
// machine's file table once - the note in `scripts/reset-native-sandbox.sh` is
// what that looked like. A step's path traffic is bursty per process, so one
// entry takes nearly all of the saving for one descriptor, and a step that
// alternates between two processes is served exactly as it was before.
//
// **Held only by the notification loop**, which is one goroutine, so there is no
// lock here and there must not come to be a second caller without one.
//
// Safe against pid reuse by construction rather than by checking: a descriptor
// on this file is bound to the task that was open, not to the number. If that
// task exits and its pid is issued again, reads through the old descriptor fail
// - they do not quietly return another process's memory - so the worst case is
// an observation declared incomplete, which is what I3 asks for and not what it
// forbids.
type memFiles struct {
	pid  uint32
	file *os.File
}

// fileFor is the open file for a process, opening it if this is a new one.
func (m *memFiles) fileFor(pid uint32) (*os.File, error) {
	if m.file != nil && m.pid == pid {
		return m.file, nil
	}

	// Whatever was held is for somebody else now.
	m.forget()

	f, err := os.Open(procRoot + "/" + strconv.FormatUint(uint64(pid), 10) + "/mem")
	if err != nil {
		return nil, fmt.Errorf("open the target's memory: %w", err)
	}

	m.pid, m.file = pid, f

	return f, nil
}

// forget closes what is held, if anything is.
//
// Called on every failed read as well as on replacement: a read that failed may
// have failed because the process ended, and holding its descriptor open is both
// a leak and a claim about something that is gone.
func (m *memFiles) forget() {
	if m.file == nil {
		return
	}

	_ = m.file.Close()
	m.file, m.pid = nil, 0
}

// Close releases the descriptor. A tracer that has stopped holds nothing.
func (m *memFiles) Close() error { m.forget(); return nil }
