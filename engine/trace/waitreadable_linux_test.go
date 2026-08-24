//go:build linux

package trace

import (
	"time"

	"golang.org/x/sys/unix"
)

// waitReadable reports whether the listener has a notification waiting.
//
// **This bounds a wait; it does not make one interruptible.** A listener can
// poll readable and the `NOTIF_RECV` that follows still block - the notification
// can be taken by another waiter, or withdrawn with the thread that made it -
// so a loop that polls before receiving is no easier to stop than one that does
// not, and a test that waited for such a loop to finish hangs just as often.
// That was tried here and reverted.
//
// What it is good for is the case where nothing arrives at all. `receive` on its
// own has nothing to bound it, so a syscall that was never trapped costs the
// package its whole timeout and reports every test in it as failed without
// naming the one that was waiting. Asking first turns that into a sentence
// (E634).
func waitReadable(fd int, within time.Duration) (bool, error) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}} //nolint:gosec // a descriptor is not that big

	for {
		n, err := unix.Poll(fds, int(within.Milliseconds()))
		if err == unix.EINTR {
			continue
		}

		if err != nil {
			return false, err
		}

		return n > 0, nil
	}
}
