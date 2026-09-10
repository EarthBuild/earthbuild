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

	if sessionIdle() <= 0 {
		t.Error("an unbounded wait leaves a VM per abandoned build")
	}

	if sessionIdle() > time.Hour {
		t.Errorf("a machine waits %v for a build that may never come", sessionIdle())
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
func TestOnlyAnExplicitYesLetsAMachineWait(t *testing.T) {
	for _, c := range []struct {
		set  string
		want bool
		why  string
	}{
		{"1", true, "the host said a later build may connect"},
		{"", false, "nothing was said, which must not mean yes"},
		{"0", false, "the host said no"},
		{"true", false, "not the one value that means yes"},
		{"yes", false, "not the one value that means yes"},
	} {
		t.Setenv(envMayRejoin, c.set)

		if got := mayRejoin(); got != c.want {
			t.Errorf("%s=%q read as mayRejoin=%v, wanted %v (%s)",
				envMayRejoin, c.set, got, c.want, c.why)
		}
	}
}

// TestSilenceMeansSingleUse states the direction of the default on its own,
// because the table above would still pass with every answer inverted
// together.
//
// **This is the whole of why the question is asked in the positive.** The first
// version asked the host to say when it could *not* come back, which makes
// waiting what happens by default - and puts a torn store behind every way of
// failing to say anything: an older host, a setting dropped from the list that
// crosses into the guest, a machine configuration written by hand. Asked this
// way round, every one of those is a machine that behaves as it always did.
func TestSilenceMeansSingleUse(t *testing.T) {
	t.Setenv(envMayRejoin, "")

	if mayRejoin() {
		t.Fatal("a guest nobody has said anything to will wait for a second" +
			" build, so its host's shutdown becomes a kill with the store" +
			" mounted")
	}
}
