package fleet_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// The scheduler is told which workers exist, or it never uses them.
//
// Placement (§4.7.1) chooses among the workers it was given: a fleet whose
// members are reachable but unlisted is a fleet that never receives a step, and
// the build looks exactly like a local one. So a Delegating has to be able to
// say who is out there.
//
// The local worker is *not* among them - the caller already has it, and
// returning it here would put it in the list twice, which §4.7.3 notices as two
// candidates with one identity.
func TestADelegatingNamesTheWorkersTheSchedulerCanUse(t *testing.T) {
	t.Parallel()

	r := &fleet.Rendezvous{}
	r.AddForTest()
	r.AddForTest()

	d := &fleet.Delegating{Local: &countingExecutor{}, Fleet: r}

	got := d.Remote()
	if len(got) != 2 {
		t.Fatalf("named %d worker(s), want 2\n  a fleet the scheduler is not"+
			" told about never receives a step", len(got))
	}

	for _, w := range got {
		if w.IsInvoker {
			t.Errorf("%q is marked as the invoker; only the local worker is"+
				" allowed to run host steps (§4.7.1)", w.ID)
		}
	}

	seen := map[string]bool{}
	for _, w := range got {
		if seen[w.ID] {
			t.Errorf("two workers share the ID %q, so a schedule cannot be"+
				" reproduced from it (§4.7.3)", w.ID)
		}

		seen[w.ID] = true
	}
}

// A transport that cannot enumerate its workers names none.
//
// InProcess has a fixed set and no rendezvous behind it; asking it who joined is
// a question with no answer, and inventing one - "probably one worker" - would
// have the scheduler place steps on something that may not exist.
func TestATransportThatCannotEnumerateNamesNobody(t *testing.T) {
	t.Parallel()

	d := &fleet.Delegating{Local: &countingExecutor{}}
	if got := d.Remote(); len(got) != 0 {
		t.Errorf("named %d worker(s) with no fleet at all", len(got))
	}
}
