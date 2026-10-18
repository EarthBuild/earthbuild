package exec_test

import (
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The scheduler and the guest agree about which caches serialise a step.
//
// Two lists over one rule: `core.ClaimOrder` decides who may be dispatched and
// `guest.LockOrder` decides who may be in the directory. They are deliberately
// separate obligations - the scheduler must not let a step wait for a cache
// while holding a build slot (E434) - but they must pick the same caches, and
// they are written over different types by different hands.
//
// Where they disagree, the failure is silent in both directions. A cache the
// guest locks and the scheduler does not claim is the slot-holding wait the
// claim exists to remove; one the scheduler claims and the guest does not lock
// is a build serialised for nothing, reported as slowness with no cause.
//
// This lives in `exec` because it is where the translation happens: the same
// package that turns `ir.Mount` into `guest.Mount` is the one that can be held
// to translating the rule with it.
func TestTheSchedulerAndTheGuestAgreeOnWhichCachesSerialise(t *testing.T) {
	t.Parallel()

	for _, m := range []ir.Mount{
		{Target: "/c", ID: "cargo", Exclusive: true},
		{Target: "/c", ID: "npm"},
		{Target: "/c", Ephemeral: true},
		{Target: "/c", ID: "npm", Ephemeral: true},
		{Target: "/c", ID: "tok", Secret: true, Exclusive: true},
		{Target: "/c", Exclusive: true},
		{Target: "/c", ID: "cargo", Exclusive: true, Persist: true},
		{Target: "/c", ID: "cargo", Exclusive: true, ReadOnly: true},
	} {
		claimed := core.ClaimOrder([]ir.Mount{m})
		locked := guest.LockOrder([]guest.Mount{{
			Target: m.Target, ID: m.ID, ReadOnly: m.ReadOnly,
			Secret: secretName(m), Ephemeral: m.Ephemeral, Exclusive: m.Exclusive,
		}})

		if !slices.Equal(claimed, locked) {
			t.Errorf("%+v: the scheduler claims %v and the guest locks %v"+
				"\n  a cache locked and not claimed is a build slot spent waiting;"+
				" one claimed and not locked is a build serialised for nothing",
				m, claimed, locked)
		}
	}
}

// secretName is the guest's spelling of ir.Mount.Secret, which is a bool there
// and the secret's name here. Only its emptiness matters to either rule.
func secretName(m ir.Mount) string {
	if m.Secret {
		return "TOKEN"
	}

	return ""
}
