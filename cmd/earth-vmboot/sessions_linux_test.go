//go:build linux

package main

import (
	"testing"
	"time"
)

// A machine serves one build after another, and stops when nobody comes.
//
// **Because the agent's stdio is the connection.** guestd reads the protocol
// from its own standard input, so the connection ending ends the agent - and
// PID 1 handing it a single accepted socket meant the machine ended with it.
// Every build therefore paid a boot, and the register that lets the next build
// find a running machine could never find one: measured, the VM was gone five
// seconds after the build that started it.
//
// Serving successive connections is what makes a machine outlive a build. What
// stops it is nobody arriving, which is the same idea as guest.EnvIdle one
// layer out - and now the layer that can act on it.
func TestTheMachineStopsWhenNobodyComes(t *testing.T) {
	t.Parallel()

	// A guest with no host is the ordinary end of a session, not a failure:
	// the last build finished and no other came.
	if !idleOut(errAcceptTimeout) {
		t.Error("a machine that waited and saw nobody does not read as idle," +
			" so it would report an error instead of stopping")
	}

	if idleOut(errNoSuchThing) {
		t.Error("a real accept failure reads as idle, so a broken vsock would" +
			" look like a quiet afternoon and the console would say nothing")
	}
}

// The wait is bounded, or a machine nobody uses lives until the host reboots.
func TestTheIdleWaitIsBounded(t *testing.T) {
	t.Parallel()

	if idleFrom(nil) <= 0 {
		t.Error("an unbounded wait leaves a VM per abandoned build")
	}

	if idleFrom(nil) > time.Hour {
		t.Errorf("a machine waits %v for a build that may never come", idleFrom(nil))
	}
}

// TestTheIdleWaitIsReadFromWhereTheHostPutIt.
//
// **The same fault as mayRejoin, in the function beside it.** The host's
// settings reach this guest on the kernel command line and `agentEnv` puts them
// into the *agent's* environment; PID 1's own never has them. So this asked
// `os.Getenv` for EARTH_GUEST_IDLE and never once saw it - the setting crossed,
// was counted by saySettings, went to the agent, and the machine's own idle
// period was the built-in default on every host that ever set it.
//
// Found by checking the neighbour of a bug rather than by anything failing,
// which is what that class of fault costs: nothing reports it, and an A/B
// between two values produces one result twice.
func TestTheIdleWaitIsReadFromWhereTheHostPutIt(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		settings []string
		want     time.Duration
		why      string
	}{
		{[]string{"EARTH_GUEST_IDLE=90s"}, 90 * time.Second, "what the host said"},
		{[]string{"EARTH_TIMINGS=1", "EARTH_GUEST_IDLE=2m"}, 2 * time.Minute, "said among others"},
		{nil, defaultSessionIdle, "nothing said"},
		{[]string{"EARTH_GUEST_IDLE="}, defaultSessionIdle, "said and empty"},
		{[]string{"EARTH_GUEST_IDLE=soon"}, defaultSessionIdle, "not a duration"},
		{[]string{"EARTH_GUEST_IDLE=0"}, defaultSessionIdle, "zero is not a wait"},
		{[]string{"EARTH_GUEST_IDLE=-5s"}, defaultSessionIdle, "nor is a negative one"},
	} {
		if got := idleFrom(c.settings); got != c.want {
			t.Errorf("%q read as %v, wanted %v (%s)", c.settings, got, c.want, c.why)
		}
	}
}

// TestOnlyAnExplicitYesLetsAMachineWait is the guard on the fault that ended
// the first attempt at reuse.
//
// **A machine that waits cannot be stopped by hanging up.** The host ends a
// build by closing the protocol connection, which ends the agent and returns
// PID 1 from `serve` so the store is unmounted on the way out. A guest that
// goes back to waiting instead does none of that: the host's shutdown times out
// after ten seconds and the VMM is killed with the store still mounted. That is
// a torn store, and it reads as `/bin/busybox is gone from the base` in a build
// that changed one Go file - 61 cache hits to none.
//
// Asked of the settings rather than of the environment, because that is where
// they are. The host's settings reach this guest on the kernel command line and
// are put into the *agent's* environment; PID 1 never sees them in its own. A
// first version of this read `os.Getenv` and was always false - every machine
// ended with its first build while its console reported the setting arriving.
func TestOnlyAnExplicitYesLetsAMachineWait(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		settings []string
		want     bool
		why      string
	}{
		{[]string{"EARTH_VM_MAY_REJOIN=1"}, true, "the host said a later build may connect"},
		{[]string{"EARTH_TIMINGS=1", "EARTH_VM_MAY_REJOIN=1"}, true, "said, among others"},
		{nil, false, "nothing was said, which must not mean yes"},
		{[]string{"EARTH_VM_MAY_REJOIN=0"}, false, "the host said no"},
		{[]string{"EARTH_VM_MAY_REJOIN=true"}, false, "not the one value that means yes"},
		{[]string{"EARTH_TIMINGS=1"}, false, "a different setting entirely"},
		{[]string{"NOT_EARTH_VM_MAY_REJOIN=1"}, false, "a name ending in the right one"},
	} {
		if got := rejoinAsked(c.settings); got != c.want {
			t.Errorf("%q read as mayRejoin=%v, wanted %v (%s)",
				c.settings, got, c.want, c.why)
		}
	}
}

// TestSilenceMeansSingleUse states the direction of the default alone, because
// the table above would pass just as well with every answer inverted together.
//
// **This is the whole of why the question is asked in the positive.** The first
// version asked the host to say when it could *not* come back, which makes
// waiting the default - and puts a torn store behind every way of failing to
// say anything: an older host, a setting dropped from the list that crosses
// into the guest, a machine configuration written by hand. Asked this way
// round, every one of those is a machine that behaves as it always did.
func TestSilenceMeansSingleUse(t *testing.T) {
	t.Parallel()

	if rejoinAsked(nil) {
		t.Fatal("a guest nobody has said anything to will wait for a second" +
			" build, so its host's shutdown becomes a kill with the store" +
			" mounted")
	}
}
