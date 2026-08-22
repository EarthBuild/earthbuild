package guest

import (
	"sync"
	"time"
)

// EnvIdle is how long a sandbox stays up with nothing to do.
//
// A duration - `20m`, `2h`, `90s`. Zero means never, which is what this engine
// did before the timeout existed; unset means DefaultIdle, because the guest is
// started by a host that supplies one.
const EnvIdle = "EARTH_GUEST_IDLE"

// DefaultIdle is how long an unattended sandbox waits before stopping.
//
// Generous, because the cost of the two mistakes is not symmetric: a sandbox
// that stops too early costs one VM boot, about 0.4s, on the next build; one
// that never stops costs a VM per interrupted build until the machine runs out
// of file descriptors. Long enough to sit through lunch and find the sandbox
// warm, short enough that a laptop closed on Friday is not still running it on
// Monday.
const DefaultIdle = 30 * time.Minute

// idle decides when a sandbox nobody is using should stop.
//
// **It lives in the guest, and that is the whole design.** The obvious place for
// this is the host - it knows when the build ended - and the obvious place is
// wrong: the host process is the one that gets killed, `defer` does not run on
// SIGKILL, and a VM whose reaper died is exactly the VM that leaks. Anything
// that cleans up a sandbox has to outlive whatever killed the build, and the
// only thing that does is the sandbox.
//
// It counts *work*, not messages. A RUN that compiles for two hours sends
// nothing while it runs, so a timer measuring silence would stop the sandbox in
// the middle of the most expensive step in the build.
type idle struct {
	after time.Duration
	now   func() time.Time

	mu   sync.Mutex
	last time.Time
	// busy is how many requests are in flight. A counter rather than a flag:
	// requests are served concurrently, and the first to finish would otherwise
	// release a hold the others still need.
	busy int
}

func newIdle(after time.Duration, now func() time.Time) *idle {
	if now == nil {
		now = time.Now
	}

	return &idle{after: after, now: now, last: now()}
}

// touch records that something arrived.
func (i *idle) touch() {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.last = i.now()
}

// begin records that work has started, and holds the sandbox open until it ends.
func (i *idle) begin() {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.busy++
	i.last = i.now()
}

// end records that work has finished. The countdown runs from here rather than
// from when the work started, so a long step buys the grace period afterwards.
func (i *idle) end() {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.busy > 0 {
		i.busy--
	}

	i.last = i.now()
}

// expired reports whether the sandbox has been unused for long enough to stop.
//
// **Zero means never, not immediately.** A misread configuration that keeps
// sandboxes alive wastes memory somebody will notice; one that stops every
// sandbox the instant it is created breaks every build on the machine, and the
// two are one typo apart.
func (i *idle) expired() bool {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.after <= 0 || i.busy > 0 {
		return false
	}

	return i.now().Sub(i.last) >= i.after
}

// The nil-safe forms, so a Server without an idle timeout needs no checks at its
// call sites. A sandbox with no timeout is a supported configuration - see
// EnvIdle - and it should not be the one that reads worse.
func (i *idle) touched() {
	if i != nil {
		i.touch()
	}
}

func (i *idle) working() {
	if i != nil {
		i.begin()
	}
}

func (i *idle) done() {
	if i != nil {
		i.end()
	}
}

// Watch stops the process once the sandbox has been unused for its period.
//
// Polled rather than timed: a timer reset on every request is a timer reset
// thousands of times a build, and the check is three field reads. The interval
// is a fraction of the period, so the sandbox outlives its welcome by at most
// that much.
//
// Exits the process rather than returning. There is nothing above this worth
// unwinding to - the guest *is* the sandbox, and a guest that returned would
// leave the VM up with nothing serving it, which is the leak this exists to
// prevent, minus the VM's only useful occupant.
func (i *idle) Watch(stop func()) {
	if i == nil || i.after <= 0 {
		return
	}

	every := i.after / 10
	if every < time.Second {
		every = time.Second
	}

	for {
		time.Sleep(every)

		if i.expired() {
			stop()

			return
		}
	}
}

// NewIdle is newIdle for the command that starts a guest.
func NewIdle(after time.Duration) *idle { return newIdle(after, nil) }
