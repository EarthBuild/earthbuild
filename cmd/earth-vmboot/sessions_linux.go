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

// envMayRejoin tells a guest that a later build may connect to it.
//
// **Because a machine that waits cannot be shut down by hanging up.** The host
// ends a build by closing the protocol connection, and that ends the agent,
// returns PID 1 from `serve`, and unmounts the store on the way out. A guest
// that goes back to waiting instead does none of it - so the host's shutdown
// times out after ten seconds and the VMM is killed with the store still
// mounted. That is a torn store, and it reads as `/bin/busybox is gone from the
// base` in a build that changed one Go file: 61 cache hits to none.
//
// **Stated in the positive, so silence is safe.** The first version of this
// asked the opposite question - the host set `EARTH_VM_ONE_SESSION=1` when it
// could *not* come back - which makes waiting the default and puts the
// dangerous answer behind every way of failing to say anything: an older host,
// a setting dropped from the list that crosses into the guest, a hand-written
// machine configuration. Each of those is a torn store. Asked this way round
// they are all a machine that behaves exactly as it did before sessions
// existed.
//
// The host says yes only where the network outlives the build, which is what
// EARTH_VM_TAP means: a tap somebody made as root does not go when the build
// that used it goes, and the stack this engine runs itself does. See
// exec.mayAttach.
const envMayRejoin = "EARTH_VM_MAY_REJOIN"

// mayRejoin reports whether a later build may connect to this machine.
//
// **Read from the command line, not from this process's environment.** The
// host's settings arrive on the kernel command line - the only channel that
// exists before the guest does - and `agentEnv` puts them into the *agent's*
// environment. PID 1's own environment never sees them. So this asked
// `os.Getenv` for something the host had said, the guest had received and
// nobody had given to the process that needed it, and every machine ended with
// its first build while the console reported the setting arriving.
//
// That is the failure `saySettings` was written for, one layer further in: not
// a setting that failed to cross, but one that crossed and was routed past its
// reader.
func mayRejoin() bool { return rejoinAsked(fromCmdline()) }

// rejoinAsked is the decision, taken apart from where the settings come from so
// it can be tested without a kernel.
//
// Anything but an explicit yes is no. A misspelt value is not a decision, and
// the cost of reading one as yes is a store.
func rejoinAsked(settings []string) bool {
	for _, kv := range settings {
		if kv == envMayRejoin+"=1" {
			return true
		}
	}

	return false
}
