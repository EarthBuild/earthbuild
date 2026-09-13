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

// contentAsker answers what layers hold, and counts the questions.
type contentReplier struct {
	holds map[ir.NodeID]ir.NodeID
	calls int
	fail  bool
}

func (c *contentReplier) content(ids []ir.NodeID) ([]ir.NodeID, error) {
	c.calls++

	if c.fail {
		return nil, errAskFailed
	}

	out := make([]ir.NodeID, len(ids))
	for i, id := range ids {
		out[i] = c.holds[id] // the zero id where it cannot say
	}

	return out, nil
}

// A layer's content is asked of whoever holds the store, and asked once.
//
// Κₜ (green paper 4.5a) names a base by what its layers hold, and on a store the
// guest owns the manifest that answers is not on the host's filesystem. Without
// this the key is never derivable there, and the tier written for rebuilt bases
// does nothing on the builds with the most to gain.
func TestContentIsAskedOfWhoeverHoldsTheStore(t *testing.T) {
	t.Parallel()

	known, unknown := ir.NodeID{1}, ir.NodeID{2}
	held := ir.NodeID{9}

	ask := &contentReplier{holds: map[ir.NodeID]ir.NodeID{known: held}}
	b := &guestBlobs{askContent: ask.content}

	got, ok := b.ContentOf(known)
	if !ok || got != held {
		t.Fatalf("answered %v/%v, want %v/true", got, ok, held)
	}

	// Asked again: remembered, not re-asked. A base is consulted once per step
	// and a build has many.
	if _, _ = b.ContentOf(known); ask.calls != 1 {
		t.Errorf("asked %d times about one layer; a round trip per lookup is the"+
			" cost this cache exists to avoid", ask.calls)
	}

	// A layer the store cannot say anything about is unknown, not zero-content.
	if _, ok := b.ContentOf(unknown); ok {
		t.Error("a layer the store could not describe was given a content id," +
			" which two bases holding anything at all would share")
	}
}

// A store that cannot be asked says nothing, and says it once.
func TestAStoreThatCannotBeAskedGivesNoContent(t *testing.T) {
	t.Parallel()

	ask := &contentReplier{fail: true}

	var said int

	b := &guestBlobs{askContent: ask.content, Why: func(error) { said++ }}

	if _, ok := b.ContentOf(ir.NodeID{1}); ok {
		t.Error("a failed question produced a content id, so a key would be" +
			" derived from an answer nobody gave")
	}

	_, _ = b.ContentOf(ir.NodeID{2})

	if said != 1 {
		t.Errorf("reported %d times; one unreachable store is one report", said)
	}
}
