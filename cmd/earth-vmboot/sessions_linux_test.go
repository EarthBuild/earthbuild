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
