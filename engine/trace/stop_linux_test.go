//go:build linux

package trace

import (
	"runtime"
	"testing"
	"time"
)

// Run returns when the tracer is stopped.
//
// The test that was missing, and its absence cost an integration hang that took
// a re-executed test binary and four dead ends to find.
//
// `receive` blocks in an `ioctl`, and **closing a descriptor does not wake a
// thread blocked in one**. That is written down in E206 - in a test comment,
// about a test that therefore never waited for this loop - and then written into
// the guest anyway, which waited for exactly that and hung every traced step.
//
// So stopping is its own mechanism rather than a side effect of closing, and
// this is the assertion that says so.
func TestRunReturnsWhenStopped(t *testing.T) {
	SkipIfAlreadyFiltered(t)

	ready := make(chan *Tracer, 1)
	failed := make(chan error, 1)

	park := parking(t)

	go func() {
		// Never unlocked; the goroutine parks so the thread outlives the check.
		runtime.LockOSThread()

		tr, err := StartOnSelf()
		if err != nil {
			failed <- err

			return
		}

		ready <- tr

		park()
	}()

	var tr *Tracer

	select {
	case err := <-failed:
		t.Skipf("no seccomp user notification here: %v", err)
	case tr = <-ready:
	}

	done := make(chan struct{})

	go func() { tr.Run(); close(done) }()

	// Long enough that Run is certainly blocked in the kernel rather than not
	// yet started, which is the state the bug needs to be visible in.
	time.Sleep(50 * time.Millisecond)

	err := tr.Close()
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after Close" +
			"\n  it is blocked in ioctl(SECCOMP_IOCTL_NOTIF_RECV), and closing" +
			" the descriptor does not wake a thread that is already in one" +
			"\n  anything joining this goroutine waits for ever")
	}
}
