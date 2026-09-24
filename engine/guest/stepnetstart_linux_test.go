//go:build linux

package guest

import "testing"

// Two earth processes do not both start numbering at one.
//
// The counter allocates the namespace name, both interface names and the /30
// subnet from a single number, and `ip netns add` failing on the name is the
// atomic claim on all four. That is right, and it means a *collision* is the
// normal case rather than the exception: a build runs a nested `earth` inside
// every step that starts one, each beginning at one and walking straight into
// the namespaces the outer build already holds (E933).
//
// The fix is not to salt the name - a salted name would take the same subnet and
// the same host-side interface, turning a loud retry into a silent overlap. It
// is to begin somewhere else, so the claim usually succeeds first time and the
// retry goes back to being the backstop it reads as.
func TestTwoProcessesDoNotBothStartAtOne(t *testing.T) {
	t.Parallel()

	// The counter, not the helper that seeds it: a test of the helper alone
	// passes while the seeding is wired to nothing, which is how the first
	// version of this let a neutered `Store` through.
	seen := map[int64]bool{}
	for range 40 {
		seen[newStepNetCounter().Load()] = true
	}

	if len(seen) < 2 {
		t.Errorf("every process starts at the same number (%v), so a nested"+
			" build collides on its first namespace and every one after it", seen)
	}

	// Inside the space the plan can address: `stepNetPlan` masks to 0x3fff, so a
	// start beyond that wraps onto blocks a live step may hold - which is the
	// silent overlap this exists to avoid.
	for at := range seen {
		if at < 0 || at > 0x3fff {
			t.Errorf("a start of %d is outside the 16384 blocks the plan addresses", at)
		}
	}
}
