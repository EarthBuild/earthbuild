package exec

import (
	"strings"
	"testing"
)

// What is on the other end of an inherited socket, and whether it may be used.
//
// The question survives whichever way the default falls: `--share-outer` opting
// in, or isolation opting out, both have to decide the same three cases, and
// only one of them is safe without an operator saying so.
func TestAnInheritedDaemonIsOnlyTakenFromAnOuterStep(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		inside    bool
		socket    bool
		allowed   bool
		usable    bool
		saysAbout string
	}{
		{
			// The nesting case, and the only one that needs no permission: the
			// socket was put there by the step this build is running inside, so
			// the daemon on the end of it is the outer *step's*, not a machine's.
			name: "inside a step, socket there", inside: true, socket: true,
			usable: true,
		},
		{
			// Not in a container, so the socket at the conventional path is the
			// machine's own daemon - which is root on the machine (E145). Every
			// image the build touches would outlive it, and a step could write
			// to any of them.
			name: "on the machine, socket there", socket: true,
			saysAbout: "this machine",
		},
		{
			// The same, said yes to by an operator, which is what that
			// permission is for.
			name: "on the machine, allowed", socket: true, allowed: true,
			usable: true,
		},
		{
			// Nothing to inherit. Refused *here*, rather than passed on to fail
			// ninety seconds later as an unreachable daemon - which reads as a
			// broken daemon rather than as one that was never there (I10).
			name: "inside a step, no socket", inside: true,
			saysAbout: "no daemon",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ok, why := outerDaemonUsable(tc.inside, tc.socket, tc.allowed)

			if ok != tc.usable {
				t.Fatalf("usable = %v, want %v (%s)", ok, tc.usable, why)
			}

			if tc.usable {
				if why != "" {
					t.Errorf("a usable daemon came with a complaint: %s", why)
				}

				return
			}

			if !strings.Contains(why, tc.saysAbout) {
				t.Errorf("the refusal does not mention %q: %s", tc.saysAbout, why)
			}
		})
	}
}
