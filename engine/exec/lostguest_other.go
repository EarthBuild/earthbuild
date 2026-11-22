//go:build !linux

package exec

import (
	"strings"
	"time"
)

// failureReporter is a sandbox that knows why its guest process ended.
type failureReporter interface{ GuestFailure() string }

// lostGuest adds how the guest ended when the failure is that it stopped
// talking.
//
// No console here, but the Apple backend has the exit status of the process
// behind `container exec`, which tells a kill from a crash. Without it every
// death read `guest connection lost: EOF`.
//
// **Asked for a moment, not once**: the pipe's EOF can reach the reader before
// `Wait` has returned in the goroutine that records the status.
func lostGuest(err error, sb Sandbox) error {
	if err == nil || !strings.Contains(err.Error(), "guest connection lost") {
		return err
	}

	r, ok := sb.(failureReporter)
	if !ok {
		return err
	}

	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		why := r.GuestFailure()
		if why != "" {
			return &lostGuestError{err: err, why: why}
		}

		if time.Now().After(deadline) {
			return err
		}
	}
}

// lostGuestError keeps the original error reachable under the added cause.
type lostGuestError struct {
	err error
	why string
}

func (l *lostGuestError) Error() string { return l.err.Error() + "\n  " + l.why }
func (l *lostGuestError) Unwrap() error { return l.err }
