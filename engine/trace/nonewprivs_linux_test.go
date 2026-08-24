//go:build linux

package trace

import (
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

// Installing a filter sets no-new-privs on the thread that installed it.
//
// **The kernel takes a filter from either a caller with CAP_SYS_ADMIN or one
// that has set `PR_SET_NO_NEW_PRIVS`, and this engine has no capabilities by
// design (E98).** So on the machine a build runs on, the prctl is what makes
// the filter possible at all - and every test of the tracer would keep passing
// without it, because the suite is often run as root and root does not need it.
// That is exactly the case a mutation sweep finds and a green suite does not:
// remove the prctl and nothing here noticed.
//
// It is also the honest setting rather than a formality. It says this thread
// cannot gain privileges through an exec, which is true of a build step, and a
// step that could would be one the tracer's observations no longer describe.
//
// Asked of the thread rather than of `install`'s return, because the flag is a
// property of a thread: a test that trusted the function to have done it would
// pass against a function that returned nil without doing anything, which is
// the mutant.
func TestInstallingAFilterSetsNoNewPrivs(t *testing.T) {
	// Not parallel: it locks a thread and installs a filter on it.
	done := make(chan int, 1)
	failed := make(chan error, 1)

	// Registered from the test's own goroutine, because `t.Cleanup` has to be
	// in place before the test can end and a worker registering it races that.
	park := parking(t)

	go func() {
		// Locked and never unlocked: a filter cannot be removed, so the thread
		// ends with this goroutine rather than going back to the runtime
		// carrying one (E627).
		runtime.LockOSThread()

		fd, err := install(auditArch, traced)
		if err != nil {
			failed <- err

			return
		}

		defer func() { _ = unix.Close(fd) }()

		// PR_GET_NO_NEW_PRIVS returns the flag as the syscall's result, which
		// unix.Prctl does not hand back - so the raw call, and only here.
		set, _, errno := unix.Syscall(unix.SYS_PRCTL, unix.PR_GET_NO_NEW_PRIVS, 0, 0)
		if errno != 0 {
			failed <- errno

			return
		}

		done <- int(set)

		// Held until the test is over and then ended, which is what destroys a
		// locked thread and takes its filter with it. `select{}` would hold it
		// for the life of the binary, which is the mistake `parking` exists to
		// stop being made again (E627).
		park()
	}()

	select {
	case err := <-failed:
		t.Skipf("no seccomp user notification here: %v", err)
	case got := <-done:
		if got != 1 {
			t.Errorf("no-new-privs is %d after installing a filter, want 1"+
				"\n  the kernel takes a filter from a caller with CAP_SYS_ADMIN"+
				" or one that has set it, and this engine has no capabilities"+
				" (E98)", got)
		}
	}
}
