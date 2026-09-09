package core

import (
	"context"
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// countingViews records how many paths it was asked to digest.
type countingViews struct {
	base  BaseView
	asked int
}

func (c *countingViews) View(context.Context, []ir.NodeID) (BaseView, error) {
	return c.base, nil
}

func (c *countingViews) ViewFor(_ context.Context, _ []ir.NodeID, want []string) (BaseView, error) {
	c.asked += len(want)

	return c.base, nil
}

// askingViews answers the staleness question itself, the way a store held
// somewhere else should.
type askingViews struct {
	why    string
	asked  int
	broken error
}

func (a *askingViews) View(context.Context, []ir.NodeID) (BaseView, error) {
	return nil, errors.New("no view without paths")
}

func (a *askingViews) WhyStaleIn(context.Context, []ir.NodeID, Observation) (string, error) {
	a.asked++

	return a.why, a.broken
}

// A store held elsewhere is asked the question, not for the evidence.
//
// **Because the comparison stops at the first difference and the fetch cannot.**
// WhyStale walks a step's observed reads in order and returns as soon as one
// has changed - on a host that is one lookup for a source file somebody just
// edited. A view that has to be fetched has no such luck: every path is
// computed before the first is compared, so a guest holding the store did 6299
// lookups to answer what the host answered with one, and that was 4.0s of a
// 4.7s build.
//
// Asking the holder of the store to run the comparison keeps one implementation
// of it - WhyStale, here - and makes the work proportional to the answer.
func TestAStoreHeldElsewhereIsAskedTheQuestion(t *testing.T) {
	// Not parallel: the path is behind a setting, because the answers it gives
	// disagree with the fetched view's. See EnvAskStale.
	t.Setenv(EnvAskStale, "1")

	obs := Observation{Reads: map[string]ir.NodeID{"/a": {}, "/b": {}, "/c": {}}}

	asking := &askingViews{why: "/a changed in the base"}

	why, err := whyStaleVia(context.Background(), asking, nil, obs)
	if err != nil {
		t.Fatal(err)
	}

	if why != "/a changed in the base" {
		t.Errorf("the holder's answer was not used: %q", why)
	}

	if asking.asked != 1 {
		t.Errorf("the holder was asked %d times, want once", asking.asked)
	}
}

// A source that cannot answer is still read the old way, so nothing that works
// today stops working.
func TestASourceThatCannotAnswerIsStillRead(t *testing.T) {
	t.Parallel()

	obs := Observation{Reads: map[string]ir.NodeID{"/a": {}}}

	counting := &countingViews{base: emptyBase{}}

	why, err := whyStaleVia(context.Background(), counting, nil, obs)
	if err != nil {
		t.Fatal(err)
	}

	if why == "" {
		t.Error("an empty base should report the read as gone")
	}

	if counting.asked == 0 {
		t.Error("the fallback did not fetch a view at all")
	}
}

// emptyBase holds nothing.
type emptyBase struct{}

func (emptyBase) Digest(string) (ir.NodeID, bool)        { return ir.NodeID{}, false }
func (emptyBase) ListingDigest(string) (ir.NodeID, bool) { return ir.NodeID{}, false }
