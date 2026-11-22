package guest

import (
	"testing"
	"time"
)

// A sandbox that nobody is using stops.
//
// The VM outlives a build on purpose - that is what makes a second build 2.9s
// rather than 3.2s - so it cannot exit when the host disconnects. It must
// therefore decide for itself when it is no longer wanted, and it must do so
// from *inside*: the host process that would otherwise clean up is exactly the
// one that gets killed, and SIGKILL catches nothing. An evening of interrupted
// builds left 17 orphaned VMs holding 59% of this machine's file descriptors,
// which presented as unrelated steps failing with `too many open files in
// system`.
func TestASandboxNobodyIsUsingStops(t *testing.T) {
	t.Parallel()

	now := time.Unix(1000, 0)
	i := newIdle(time.Minute, func() time.Time { return now })

	if i.expired() {
		t.Fatal("expired before any time passed")
	}

	now = now.Add(59 * time.Second)

	if i.expired() {
		t.Error("expired before its period was up")
	}

	now = now.Add(2 * time.Second)

	if !i.expired() {
		t.Error("the period passed with no activity and it is still running")
	}
}

// Work in flight holds it open, however long the work takes.
//
// A `RUN` that compiles for two hours sends no protocol traffic while it runs.
// Measuring silence rather than idleness would kill the sandbox in the middle of
// the longest, most expensive step in the build - the one where losing the work
// costs most.
//
// *A wall-clock threshold measures the machine.* The remedy is not a bigger
// threshold; it is to count what is happening rather than what is arriving.
func TestWorkInFlightHoldsTheSandboxOpen(t *testing.T) {
	t.Parallel()

	now := time.Unix(1000, 0)
	i := newIdle(time.Minute, func() time.Time { return now })

	i.begin()

	now = now.Add(6 * time.Hour)

	if i.expired() {
		t.Fatal("a step was running and the sandbox stopped underneath it")
	}

	i.end()

	// The countdown starts when the work ended, not when it began.
	now = now.Add(30 * time.Second)

	if i.expired() {
		t.Error("expired 30s after a step finished, with a minute's grace configured")
	}

	now = now.Add(31 * time.Second)

	if !i.expired() {
		t.Error("a minute after the last step finished and it is still running")
	}
}

// Anything arriving is activity.
func TestArrivingWorkResetsTheClock(t *testing.T) {
	t.Parallel()

	now := time.Unix(1000, 0)
	i := newIdle(time.Minute, func() time.Time { return now })

	now = now.Add(50 * time.Second)
	i.touch()
	now = now.Add(50 * time.Second)

	if i.expired() {
		t.Error("a request arrived 50s ago and it expired anyway")
	}
}

// Turning it off is allowed, and is the only way to get the old behaviour.
//
// Somebody debugging a guest wants it to stay up; somebody on a shared builder
// wants it gone in minutes. The default cannot be right for both, so the knob is
// real - and zero means never, rather than meaning immediately, because a
// misread configuration that kills every sandbox at once is worse than one that
// keeps them.
func TestZeroMeansNever(t *testing.T) {
	t.Parallel()

	now := time.Unix(1000, 0)
	i := newIdle(0, func() time.Time { return now })

	now = now.Add(30 * 24 * time.Hour)

	if i.expired() {
		t.Error("idle timeout is off and it expired anyway")
	}
}

// Nested work is still work.
//
// Requests are served concurrently, so several may be in flight at once. A
// counter rather than a flag: the first to finish would otherwise clear the hold
// while the others are still running.
func TestTheLastPieceOfWorkReleasesTheHold(t *testing.T) {
	t.Parallel()

	now := time.Unix(1000, 0)
	i := newIdle(time.Minute, func() time.Time { return now })

	i.begin()
	i.begin()
	i.end()

	now = now.Add(2 * time.Minute)

	if i.expired() {
		t.Fatal("one of two steps finished and the sandbox stopped under the other")
	}

	i.end()

	now = now.Add(2 * time.Minute)

	if !i.expired() {
		t.Error("both finished long ago and it is still running")
	}
}
