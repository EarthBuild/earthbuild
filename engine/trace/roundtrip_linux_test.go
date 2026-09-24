//go:build linux

package trace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A trapped open is seen, answered, and proceeds.
//
// The whole loop, on one process. A filter applies to the thread that installs
// it and to whatever that thread spawns - there is no `TSYNC` here - so a
// goroutine that locks its thread can be filtered while the rest of the test
// process is not. That removes the fork-exec choreography entirely, and with it
// the temptation to test the pieces separately and assume they compose.
//
// The reading goroutine must not be the filtered one, and the reason is worth
// stating: a `NOTIF_RECV` blocks until a call arrives, and if the thread doing
// the reading were itself filtered, its own next `openat` would trap waiting for
// an answer only it could give. Which is a deadlock, and would present as a
// hung test with no output.
//
// The Go runtime is on that thread too, and its own opens trap alongside the
// test's. The reader answers everything with CONTINUE, which is the engine's
// policy anyway, so this is the arrangement a real step runs in rather than a
// simplification of it.
func TestATrappedOpenIsSeenAndProceeds(t *testing.T) {
	// Not parallel: this installs a seccomp filter on a thread of the test
	// binary, and a filtered thread the runtime hands to another test would
	// trap that test's syscalls instead.
	dir := t.TempDir()

	path := filepath.Join(dir, "wanted.txt")

	err := os.WriteFile(path, []byte("content"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	listener := make(chan int, 1)
	failed := make(chan error, 1)
	opened := make(chan error, 1)

	go func() {
		// Locked and **never unlocked**, which is the opposite of the reflex.
		//
		// A seccomp filter cannot be removed, so this thread is filtered for as
		// long as it exists. `runtime.LockOSThread` documents that a goroutine
		// exiting *without* unlocking causes its thread to be terminated - so
		// leaving it locked is what destroys the thread and contains the filter.
		// `defer runtime.UnlockOSThread()`, which is what one writes without
		// thinking, hands a permanently filtered thread back to the scheduler,
		// and the next goroutine to land on it inherits a filter with no
		// listener - every open it makes then fails ENOSYS (E206).
		runtime.LockOSThread()

		fd, err := install(auditArch, traced)
		if err != nil {
			failed <- err

			return
		}

		listener <- fd

		// The call under observation. From here every path this thread touches
		// traps, including any the runtime makes.
		_, err = os.ReadFile(path)
		opened <- err
	}()

	var fd int

	select {
	case err := <-failed:
		t.Skipf("no seccomp user notification here: %v", err)
	case fd = <-listener:
	case <-time.After(10 * time.Second):
		t.Fatal("the filter was never installed")
	}

	// **Deliberately not closed.** The answering loop below parks in
	// `NOTIF_RECV`, which no close wakes, so at the end of this test a thread is
	// still referring to this number. Releasing it hands that number to the next
	// listener installed in this process, and from that moment the parked thread
	// is a second waiter on somebody else's listener - the kernel gives a
	// notification to one waiter, so the test that owns it waits for something
	// already delivered. That is what hung `engine/trace` for the full
	// five-minute timeout in CI, with two threads parked on fd 19 and only one
	// test still running (E634).
	//
	// Keeping the descriptor costs one number for the life of the binary and
	// makes the aliasing impossible, which is the whole of the bug. Waiting for
	// the loop instead does not work and is not a near miss: poll can report a
	// listener readable and the `NOTIF_RECV` that follows still block, so a test
	// that waits for the loop to finish hangs exactly as often.

	// Answer everything, and remember whether the open was among it. The
	// runtime's own calls arrive on the same listener and are indistinguishable
	// from the test's until the syscall number is looked at, which is the
	// position the real tracer is in too.
	//
	// It is never asked to *finish*. A `NOTIF_RECV` blocks in the kernel, no
	// close wakes it, and polling first does not help - a listener can poll
	// readable and the receive after it still block. It reports the moment it
	// has the answer instead, and is left to be torn down with the process,
	// which is safe only because the descriptor above is never released.
	sawOpen := make(chan struct{}, 1)

	go func() {
		for {
			n, err := receive(fd)
			if err != nil {
				return
			}

			if isOpen(n.Data.NR) {
				// Non-blocking: this fires once and the test may already have
				// stopped listening.
				select {
				case sawOpen <- struct{}{}:
				default:
				}
			}

			// Checked after reading the notification and before answering it,
			// which is where the real tracer will read the path from.
			if !stillRunning(fd, n.ID) {
				continue
			}

			err = respond(fd, n.ID)
			if err != nil {
				return
			}
		}
	}()

	select {
	case err := <-opened:
		if err != nil {
			t.Fatalf("the trapped open did not proceed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the open never returned; nothing answered its notification")
	}

	select {
	case <-sawOpen:
	case <-time.After(10 * time.Second):
		t.Error("a file was read and no open was trapped;" +
			" the filter is installed but not seeing what it should")
	}
}

// isOpen reports whether a syscall number is one that opens a path.
//
// Not every traced call: this asks the narrower question the test above needs,
// which is whether *the read* was seen rather than whether anything was.
func isOpen(nr int32) bool {
	for _, o := range openers {
		if nr == int32(o) {
			return true
		}
	}

	return false
}

// The listener refuses a buffer that was not cleared.
//
// `NOTIF_RECV` requires the structure handed to it be zeroed, and answers
// `EINVAL` for one still carrying the last notification. `receive` allocates
// inside its retry loop for exactly that reason, and this pins the kernel
// behaviour that makes it necessary - a version that reused the buffer would
// work until the first `EINTR` and then fail for a reason with nothing to do
// with the interruption.
func TestTheListenerRefusesADirtyBuffer(t *testing.T) {
	dir := t.TempDir()

	ready := make(chan int, 1)
	failed := make(chan error, 1)
	done := make(chan struct{})

	go func() {
		// Locked and never unlocked - see the note above.
		runtime.LockOSThread()

		fd, err := install(auditArch, traced)
		if err != nil {
			failed <- err

			return
		}

		ready <- fd

		// Something to trap, then wait to be released so the notification is
		// still outstanding while the assertion below runs.
		_, _ = os.Stat(filepath.Join(dir, "absent"))
		<-done
	}()

	var fd int

	select {
	case err := <-failed:
		t.Skipf("no seccomp user notification here: %v", err)
	case fd = <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the filter was never installed")
	}

	defer func() { close(done); _ = unix.Close(fd) }()

	// Asked first, so a notification that never arrives is a sentence rather
	// than the five-minute timeout it used to be: `receive` blocks in the kernel
	// with nothing to bound it, and a test binary that hangs reports the whole
	// package as failed without naming what was waiting (E634).
	awake, pollErr := waitReadable(fd, 30*time.Second)
	if pollErr != nil {
		t.Fatalf("waiting for the notification: %v", pollErr)
	}

	if !awake {
		t.Fatal("no notification arrived in thirty seconds, so the traced" +
			" thread's stat was never trapped - or something else took it")
	}

	n, err := receive(fd)
	if err != nil {
		t.Fatalf("receiving: %v", err)
	}

	// The same buffer, uncleared, offered back.
	dirty := n

	errno := receiveInto(fd, &dirty)
	if !errors.Is(errno, unix.EINVAL) {
		t.Errorf("a dirty buffer was accepted with %v; `receive` clears one"+
			" per attempt because the kernel is expected to refuse it", errno)
	}

	err = respond(fd, n.ID)
	if err != nil {
		t.Fatalf("answering: %v", err)
	}
}
