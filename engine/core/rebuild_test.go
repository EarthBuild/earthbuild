package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// unobtainable refuses a step once, naming an input it cannot get, then works.
type unobtainable struct {
	missing ir.NodeID
	refused bool
	runs    []ir.NodeID
	made    map[ir.NodeID]ir.NodeID
}

func (u *unobtainable) Run(
	_ context.Context, n *ir.Node, _ core.Worker, base []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	u.runs = append(u.runs, n.ID())

	for _, id := range base {
		if id == u.missing && !u.refused {
			u.refused = true

			return core.Result{}, core.MissingInput{Layer: id}
		}
	}

	return core.Result{Layer: u.made[n.ID()]}, nil
}

// A base that cannot be fetched is rebuilt, not a failed build.
//
// A fleet must not be a single point of failure. When the driver cannot bring a
// delegated result back - a worker behind a firewall, a machine that left, a
// network that went away - the layer is unobtainable and **not nonexistent**:
// the step that produced it is still in the graph and can be run again (E278).
//
// Every other source in this engine degrades rather than fails (I6, I11), and
// this is the one that did not.
func TestAnUnobtainableBaseIsRebuiltRatherThanFailingTheBuild(t *testing.T) {
	t.Parallel()

	first := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"make", "base"}}}
	second := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"make", "thing"}},
		Inputs: []*ir.Node{first},
	}

	made := ir.NodeID{42}

	x := &unobtainable{
		missing: made,
		made:    map[ir.NodeID]ir.NodeID{first.ID(): made, second.ID(): {43}},
	}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "me", IsInvoker: true}},
		Executor: x,
		Blobs:    allBlobs{},
		Record:   &core.Record{},
	}

	_, err := s.Run(t.Context(), &ir.Graph{Root: second})
	if err != nil {
		t.Fatalf("a base that could not be fetched failed the build: %v"+
			"\n  the step that made it is still in the graph", err)
	}

	// The producing step ran twice: once where its result went out of reach,
	// and once here so that what needed it could proceed.
	n := 0

	for _, id := range x.runs {
		if id == first.ID() {
			n++
		}
	}

	if n != 2 {
		t.Errorf("the producing step ran %d time(s), want 2 - once away and"+
			" once here", n)
	}
}

// A rebuild is attempted once, and then the failure stands.
//
// An input that stays unobtainable after the step that makes it has been run
// here is not a transfer problem, and retrying for ever would turn a broken
// build into a hanging one. The second failure is reported as itself.
func TestARebuildIsAttemptedOnceAndThenTheFailureStands(t *testing.T) {
	t.Parallel()

	first := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"make", "base"}}}
	second := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"make", "thing"}},
		Inputs: []*ir.Node{first},
	}

	made := ir.NodeID{42}

	x := &alwaysMissing{
		missing: made,
		made:    map[ir.NodeID]ir.NodeID{first.ID(): made, second.ID(): {43}},
	}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "me", IsInvoker: true}},
		Executor: x,
		Blobs:    allBlobs{},
		Record:   &core.Record{},
	}

	_, err := s.Run(t.Context(), &ir.Graph{Root: second})
	if err == nil {
		t.Fatal("an input that is never obtainable produced a successful build")
	}

	if !errors.As(err, &core.MissingInput{}) && !errors.Is(err, core.ErrInputMissing) {
		t.Errorf("%v\n  the reported failure should still say what could not be"+
			" obtained", err)
	}

	if x.tries > 4 {
		t.Errorf("the producing step ran %d times; one rebuild, then the"+
			" failure stands", x.tries)
	}
}

type alwaysMissing struct {
	missing ir.NodeID
	tries   int
	made    map[ir.NodeID]ir.NodeID
}

func (a *alwaysMissing) Run(
	_ context.Context, n *ir.Node, _ core.Worker, base []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	a.tries++

	for _, id := range base {
		if id == a.missing {
			return core.Result{}, core.MissingInput{Layer: id}
		}
	}

	return core.Result{Layer: a.made[n.ID()]}, nil
}
