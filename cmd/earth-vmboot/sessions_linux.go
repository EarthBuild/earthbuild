//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// errAcceptTimeout is nobody arriving before the machine gave up waiting.
//
// A sentinel rather than a timeout error from the poll, because the caller has
// to tell "the last build finished and no other came" - which is how a machine
// is meant to end - from a vsock that broke, which is worth a console line.
var errAcceptTimeout = errors.New("no host connected")

// errNoSuchThing stands in for a real failure in the tests beside this.
var errNoSuchThing = errors.New("vsock is not there")

// idleOut reports whether an accept ended because nobody came.
func idleOut(err error) bool { return errors.Is(err, errAcceptTimeout) }

// sessionIdle is how long a machine waits for the next build before stopping.
//
// **The same idea as guest.EnvIdle, at the layer that can act on it.** That
// setting stops an agent that has nothing to do; this stops the machine the
// agent was running in, which until now ended with every build anyway. Read
// from the same setting so one number governs both, and defaulted rather than
// required because a guest is started by a host that may say nothing.
func sessionIdle() time.Duration {
	at := os.Getenv(envGuestIdle)
	if at == "" {
		return defaultSessionIdle
	}

	d, err := time.ParseDuration(at)
	if err != nil || d <= 0 {
		return defaultSessionIdle
	}

	return d
}

// envGuestIdle is guest.EnvIdle, named here rather than imported: this binary
// is PID 1 of an initramfs and links nothing it does not need.
const envGuestIdle = "EARTH_GUEST_IDLE"

// defaultSessionIdle is generous for the same reason the agent's is: a machine
// that stops too early costs the next build a boot, and one that never stops
// costs a VM until somebody notices.
const defaultSessionIdle = 20 * time.Minute

// acceptWithin waits for a host, giving up after the idle period.
//
// Polled rather than blocked, because an unbounded accept is a machine that
// outlives every build that could have used it.
func acceptWithin(fd int, within time.Duration) (*os.File, error) {
	deadline := time.Now().Add(within)

	for {
		left := time.Until(deadline)
		if left <= 0 {
			return nil, errAcceptTimeout
		}

		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}} //nolint:gosec // a listening descriptor

		n, err := unix.Poll(fds, int(left.Milliseconds()))
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}

			return nil, fmt.Errorf("wait for a host on vsock: %w", err)
		}

		if n == 0 {
			return nil, errAcceptTimeout
		}

		conn, _, err := unix.Accept(fd)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
				continue
			}

			return nil, fmt.Errorf("accept on vsock: %w", err)
		}

		return os.NewFile(uintptr(conn), "vsock"), nil
	}
}

// envOneSession tells a guest that nothing will connect to it again.
//
// **Because a machine that waits cannot be shut down by hanging up.** The host
// ends a build by closing the protocol connection, and before this loop existed
// that ended the agent, returned PID 1 from serve, and unmounted the store on
// the way out. A guest that waits for the next connection instead does none of
// that - so the host's shutdown timed out after ten seconds and killed the VMM
// with the store still mounted, which is how `/bin/busybox is gone from the
// base` arrives in a build that changed one Go file.
//
// The host knows at boot whether it will come back: see exec.mayAttach. Where
// it will not, it says so here and the machine ends with its build, exactly as
// it did before.
const envOneSession = "EARTH_VM_ONE_SESSION"

// oneSession reports whether this machine serves a single build.
func oneSession() bool { return os.Getenv(envOneSession) == "1" }
