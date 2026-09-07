package cli

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// asker counts what it was asked, so a test can say the store was consulted
// once rather than once per lookup.
type asker struct {
	held  map[ir.NodeID]bool
	calls int
	fail  bool
}

func (a *asker) has(ids []ir.NodeID) ([]ir.NodeID, error) {
	a.calls++

	if a.fail {
		return nil, errAskFailed
	}

	var out []ir.NodeID

	for _, id := range ids {
		if a.held[id] {
			out = append(out, id)
		}
	}

	return out, nil
}

// TestPresenceIsAskedOfWhoeverHoldsTheStore.
//
// **A store on the guest's device is not on the host's filesystem**, and
// `Lookup` refuses an entry whose layer the blob store cannot find - so a host
// that stats its own root reads an empty answer and rebuilds everything it
// already had. `KindStoreHas`'s own comment said that one question before it
// happened; this is the answer.
func TestPresenceIsAskedOfWhoeverHoldsTheStore(t *testing.T) {
	t.Parallel()

	held, absent := ir.NodeID{1}, ir.NodeID{2}
	a := &asker{held: map[ir.NodeID]bool{held: true}}

	b := &guestBlobs{ask: a.has}

	if !b.Has(held) {
		t.Error("a layer the store holds was reported missing, so every lookup" +
			" for it misses and the work is done again")
	}

	if b.Has(absent) {
		t.Error("a layer the store does not hold was reported present, which is" +
			" a cache hit on a result nobody can materialise")
	}
}

// TestOneQuestionPerLayerHoweverOftenItIsAsked: a lookup happens per step, and
// a round trip to the guest per step per layer would cost more than the tier
// saves.
func TestOneQuestionPerLayerHoweverOftenItIsAsked(t *testing.T) {
	t.Parallel()

	held := ir.NodeID{1}
	a := &asker{held: map[ir.NodeID]bool{held: true}}
	b := &guestBlobs{ask: a.has}

	for range 5 {
		if !b.Has(held) {
			t.Fatal("a layer stopped being present")
		}
	}

	if a.calls != 1 {
		t.Errorf("asked %d times about one layer, want 1", a.calls)
	}
}

// TestAnAbsentLayerIsAskedAboutAgain: absence is not remembered. A layer the
// store does not hold yet is one this build is about to place, and remembering
// "no" would deny every later lookup of a layer that has since arrived.
func TestAnAbsentLayerIsAskedAboutAgain(t *testing.T) {
	t.Parallel()

	id := ir.NodeID{3}
	a := &asker{held: map[ir.NodeID]bool{}}
	b := &guestBlobs{ask: a.has}

	if b.Has(id) {
		t.Fatal("absent read as present")
	}

	a.held[id] = true

	if !b.Has(id) {
		t.Error("a layer that arrived after the first question is still reported" +
			" missing, so nothing placed during a build can ever be reused in it")
	}
}

// TestAStoreThatCannotBeAskedSaysNo: a miss means "do the work", which is
// always correct. Reporting present on a failed question would be a hit on a
// result that may not exist (I4).
func TestAStoreThatCannotBeAskedSaysNo(t *testing.T) {
	t.Parallel()

	a := &asker{held: map[ir.NodeID]bool{{1}: true}, fail: true}
	b := &guestBlobs{ask: a.has}

	if b.Has(ir.NodeID{1}) {
		t.Error("a store that could not be asked reported a layer present")
	}
}
