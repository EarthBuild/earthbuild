//go:build linux

package trace

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

// TestTheMemoryFileIsKeptForOneProcessAtATime.
//
// **Opening `/proc/<pid>/mem` is 4.25µs and the whole handler is 6.9µs**, so
// two thirds of what a traced call costs after the crossing is opening and
// closing a file this engine opened for the last call as well (E681).
//
// One process at a time rather than a map of them, and that is a deliberate
// bound: a step forks thousands of processes and a cache with an entry each
// holds a descriptor each. This engine has already overflowed a machine's file
// table once - see `scripts/reset-native-sandbox.sh` - and a step's traffic is
// bursty per process anyway, so one entry takes nearly all of the saving and
// costs one descriptor.
//
// The pid changing must close what it replaces. A descriptor left open is a
// leak of exactly the kind above, and one that outlived its process would also
// be a claim about a process that no longer exists.
func TestTheMemoryFileIsKeptForOneProcessAtATime(t *testing.T) {
	t.Parallel()

	var m memFiles

	defer func() { _ = m.Close() }()

	self := uint32(os.Getpid()) //nolint:gosec // a pid is not negative

	first, err := m.fileFor(self)
	if err != nil {
		t.Fatalf("opening this process's memory: %v", err)
	}

	again, err := m.fileFor(self)
	if err != nil {
		t.Fatalf("opening this process's memory a second time: %v", err)
	}

	if first != again {
		t.Error("the same process was opened twice, so nothing is cached" +
			"\n  and two thirds of the handler is still an open and a close")
	}

	// Somebody else to switch to, alive for as long as this needs it.
	other := exec.Command("sleep", "30")

	err = other.Start()
	if err != nil {
		t.Skipf("no `sleep` to hold a second pid: %v", err)
	}

	defer func() { _ = other.Process.Kill(); _, _ = other.Process.Wait() }()

	third, err := m.fileFor(uint32(other.Process.Pid)) //nolint:gosec // a pid is not negative
	if err != nil {
		t.Fatalf("opening another process's memory: %v", err)
	}

	if third == first {
		t.Fatal("two different processes were served the same memory file")
	}

	// And the one it replaced is closed, not merely forgotten.
	_, err = first.ReadAt(make([]byte, 1), 0)
	if !errors.Is(err, os.ErrClosed) {
		t.Errorf("the replaced memory file reads with %v, want it closed"+
			"\n  a descriptor per process this engine has finished with is the"+
			"\n  leak this cache is bounded to avoid", err)
	}
}

// TestForgettingAMemoryFileClosesIt.
//
// The handler forgets one when a read fails, which is how a process that has
// gone stops being asked. Left open it would be a descriptor held against a
// task that no longer exists, and the next call for that pid would be answered
// from the stale one.
func TestForgettingAMemoryFileClosesIt(t *testing.T) {
	t.Parallel()

	var m memFiles

	self := uint32(os.Getpid()) //nolint:gosec // a pid is not negative

	f, err := m.fileFor(self)
	if err != nil {
		t.Fatalf("opening this process's memory: %v", err)
	}

	m.forget()

	_, err = f.ReadAt(make([]byte, 1), 0)
	if !errors.Is(err, os.ErrClosed) {
		t.Errorf("a forgotten memory file reads with %v, want it closed", err)
	}

	// And forgetting nothing is not an error, because the handler forgets on
	// every failed read without knowing whether there was anything to forget.
	m.forget()
}
