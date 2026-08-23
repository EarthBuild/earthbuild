//go:build linux

package trace

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The listener survives a garbage collection.
//
// `InstallOnSelf` returns an `*os.File`, and an `*os.File` closes its descriptor
// from a finaliser. A tracer that kept only the number would have that
// descriptor closed underneath it the moment the file became unreachable - and
// then the number is handed out again, so `Close` closes **somebody else's**
// open file.
//
// That is not a hypothetical either. It presented as
// `readdirent …: bad file descriptor` in an unrelated capture, four runs in five,
// which is what a descriptor being yanked out from under a directory read looks
// like (E215).
//
// Forcing collections is the whole test: without them the file stays reachable
// for the length of a short test and the bug never appears.
func TestTheListenerSurvivesAGarbageCollection(t *testing.T) {
	SkipIfAlreadyFiltered(t)

	dir := t.TempDir()

	target := filepath.Join(dir, "after-gc-4e2a.txt")

	err := os.WriteFile(target, []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	ready := make(chan *Tracer, 1)
	failed := make(chan error, 1)
	opened := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		runtime.LockOSThread()

		tr, err := StartOnSelf()
		if err != nil {
			failed <- err

			return
		}

		ready <- tr
		<-opened

		f, err := os.Open(target)
		if err == nil {
			_ = f.Close()
		}

		close(finished)

		select {}
	}()

	var tr *Tracer

	select {
	case err := <-failed:
		t.Skipf("no seccomp user notification here: %v", err)
	case tr = <-ready:
	}

	go tr.Run()

	// Anything holding the listener only as a number loses it here.
	for range 3 {
		runtime.GC()
	}

	close(opened)

	// Long enough for the open to have happened and been answered. It will not
	// appear in the sightings, and that is correct: `StartOnSelf` disregards the
	// installing thread's own syscalls, and here that thread *is* the one doing
	// the opening (E211). Asserting it had been observed would have been
	// asserting the opposite of a rule two experiments old.
	time.Sleep(200 * time.Millisecond)

	// The descriptor must still be the listener. `NOTIF_ID_VALID` on a
	// notification that does not exist answers ENOENT from a *live* listener,
	// and EBADF from one that has been closed - so the two are distinguishable
	// without needing a notification in hand.
	var id uint64

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(tr.fd),
		uintptr(uint(unix.SECCOMP_IOCTL_NOTIF_ID_VALID)),
		uintptr(unsafePointerTo(&id)))

	if errno == unix.EBADF {
		t.Error("the listener was closed by a finaliser: the tracer kept the" +
			" descriptor's number and not the file that owns it" +
			"\n  the number is then reused, and Close closes whatever got it")
	}

	// And the tracer is still usable rather than merely open: a listener whose
	// reader had fallen out of Run would leave the step stopped in the kernel,
	// so the open above must have completed.
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Error("the open never returned, so nothing answered its notification" +
			" - the descriptor survived and the reader did not")
	}
}
