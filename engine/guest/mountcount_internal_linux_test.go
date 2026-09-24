//go:build linux

package guest

import "testing"

// TestHowManyMountsAStepCosts.
//
// **A ratchet on the thing that caps step throughput, not on a stopwatch.** A
// wide build ceilings near 175 steps a second, and what does not overlap is
// roughly 4ms a step of which `bindMounts` is 3.2ms - about 115us for each of
// the mounts a step makes, every one of them taking the kernel's mount lock
// (E812, E813, E814).
//
// Timing that is hopeless: the run-to-run spread is around 28%, so a threshold
// loose enough not to flake is loose enough to miss a doubling. The count is
// exact, it is the quantity the cost is proportional to, and a twelfth mount
// arriving unnoticed is precisely the regression worth catching.
//
// Raising this number is allowed. Raising it silently is not: say in the commit
// what the new mount buys, the same way `SKIP_CEILING` in the Earthfile makes
// each increment name itself.
func TestHowManyMountsAStepCosts(t *testing.T) {
	t.Parallel()

	// What a step gets before anything it asked for: the device room, shared
	// memory, and the nodes that cannot be `mknod`-ed inside a user namespace
	// and so have to be bound one at a time.
	want := []string{
		"/dev",
		"/dev/shm",
		"/dev/null",
		"/dev/zero",
		"/dev/full",
		"/dev/random",
		"/dev/urandom",
		"/dev/tty",
	}

	got := deviceMounts()

	// The device nodes are skipped when the guest does not have them, so a
	// short list here is a guest without `/dev/tty` rather than a regression -
	// but a *longer* one is always something new.
	if len(got) > len(want) {
		var targets []string
		for _, m := range got {
			targets = append(targets, m.Target)
		}

		t.Fatalf("a step now makes %d device mounts, was %d:\n  %v"+
			"\n  each one is about 115us and takes the kernel's mount lock, which is"+
			"\n  the quantity that caps step throughput on a wide build (E814)."+
			"\n  If the new mount is worth it, say what it buys and raise `want`.",
			len(got), len(want), targets)
	}

	for i, m := range got {
		if m.Target != want[i] {
			t.Errorf("device mount %d is %q, want %q"+
				"\n  order matters: /dev/shm lands inside the /dev above it,"+
				"\n  and a node bound before its directory is hidden by it",
				i, m.Target, want[i])
		}
	}
}
