package fleet

import (
	"context"
	"io"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// countingKeeper records how often it is asked about a layer.
type countingKeeper struct{ asked int }

func (k *countingKeeper) Has(ir.NodeID) bool { k.asked++; return true }

func (k *countingKeeper) Put(io.Reader) (ir.NodeID, int64, error) {
	return ir.NodeID{}, 0, nil
}

// A build with nothing delegated does not go looking.
//
// `bringBack` fetches inputs this machine lacks from whoever holds them, and
// nearly always there is nothing to do: a build with no fleet, a fleet sharing
// one store, a step whose inputs this machine made itself. The early return is
// what makes that case free - "no reason to open a connection to find that out".
//
// Without it the step still reaches `Provision`, which begins by asking the
// store about every layer the assignment stands on. That is a scan per step of
// every build there is, in exchange for nothing, and the mutant that deletes the
// shortcut survived both `go test ./engine/fleet/` and `tests/fleet+all` run
// against a fleet that really delegated - so neither suite was watching.
//
// The keeper counts. A shortcut that is taken asks it nothing at all, which is
// the only difference between the two versions from outside.
func TestNothingDelegatedAsksTheStoreNothing(t *testing.T) {
	t.Parallel()

	k := &countingKeeper{}

	d := &Delegating{Store: k}

	base := []ir.NodeID{{1}, {2}}

	err := d.bringBack(context.Background(), base, nil)
	if err != nil {
		t.Fatalf("bringBack with nothing delegated: %v", err)
	}

	if k.asked != 0 {
		t.Errorf("the store was asked about %d layer(s) for a step whose"+
			" inputs came from nowhere: the shortcut that makes a build with"+
			" no fleet cost nothing is gone, and this is a scan per step",
			k.asked)
	}
}
