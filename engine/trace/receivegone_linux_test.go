//go:build linux

package trace

import (
	"testing"

	"golang.org/x/sys/unix"
)

// A notification that evaporates is not a failure of the loop that was waiting
// for it.
//
// ENOENT from SECCOMP_IOCTL_NOTIF_RECV means the target thread was killed as the
// notification was being generated, or its blocked syscall was interrupted by a
// signal handler - the kernel says so in seccomp_unotify(2). There is nothing to
// answer and nothing to record; the next notification is the work.
//
// Treating it as fatal ended the loop, and a filter that outlives its servicer
// stops the step's next syscall in the kernel with nothing coming to release it.
// The step being traced when this was caught was `go mod download`, and the Go
// runtime signals its own threads constantly for preemption - so the window is
// not the rarity it looks (E523).
func TestAVanishedNotificationIsNotAFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		errno unix.Errno
	}{
		{"the target died mid-notification", unix.ENOENT},
		{"a signal arrived", unix.EINTR},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0

			got, err := receiveWith(func(n *seccompNotif) unix.Errno {
				calls++
				if calls == 1 {
					return tc.errno
				}

				n.ID = 42

				return 0
			})
			if err != nil {
				t.Fatalf("the loop gave up on a recoverable %v: %v", tc.errno, err)
			}

			if got.ID != 42 {
				t.Errorf("notification ID %d, want the one after the %v", got.ID, tc.errno)
			}

			if calls != 2 {
				t.Errorf("received %d times, want a retry after the %v", calls, tc.errno)
			}
		})
	}
}

// Anything else still stops the loop: a listener that is genuinely broken must
// not be retried for ever in silence.
func TestARealReceiveFailureStillStops(t *testing.T) {
	t.Parallel()

	_, err := receiveWith(func(*seccompNotif) unix.Errno { return unix.EBADF })
	if err == nil {
		t.Fatal("a broken listener was treated as recoverable")
	}
}
