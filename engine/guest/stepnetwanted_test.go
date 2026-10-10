package guest

import "testing"

// A step that asked for no network is not given one.
//
// **Because the two mechanisms disagree, and the later one wins.** `isolate`
// honours `RUN --network=none` by giving the step an empty network namespace
// through CLONE_NEWNET. The per-step network - added so two parallel steps
// could not collide on a fixed port - hands the shim a namespace the guest
// prepared, and the shim joins it with setns *after* the clone. A step that
// asked for no network therefore got a working one, and
// `tests/no-network.earth`, which the tree declares must fail, succeeded under
// the microVM while failing correctly under namespaces.
//
// The isolation is the older promise and the one a build relies on, so the
// network a step never asked for is the thing that gives way.
func TestAStepThatAskedForNoNetworkIsGivenNone(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name    string
		dropNet bool
		noNet   bool
		want    bool
	}{
		{"an ordinary step gets its own network", false, false, true},
		{"RUN --network=none gets none", false, true, false},
		{"a hermetic server gives none to anything", true, false, false},
		{"both, which is neither", true, true, false},
	} {
		if got := wantsStepNet(c.dropNet, c.noNet); got != c.want {
			t.Errorf("%s: wantsStepNet(dropNet=%v, noNet=%v) = %v, want %v",
				c.name, c.dropNet, c.noNet, got, c.want)
		}
	}
}
