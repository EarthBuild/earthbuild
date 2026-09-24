package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// driftingProducer makes a different layer the second time it is asked.
//
// Which is what I1 says cannot happen - the same step over the same inputs is
// the same layer - so a driver that does it is describing a broken world. That
// is the point: the check under test is the one that notices.
type driftingProducer struct {
	producer, consumer ir.NodeID
	wanted, drifted    ir.NodeID
	runs               map[ir.NodeID]int
	refused            bool
}

func (d *driftingProducer) Run(
	_ context.Context, n *ir.Node, _ core.Worker, base []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	if d.runs == nil {
		d.runs = map[ir.NodeID]int{}
	}

	d.runs[n.ID()]++

	if n.ID() == d.producer {
		// The first result goes out of reach; the rebuild produces something
		// else entirely.
		if d.runs[n.ID()] == 1 {
			return core.Result{Layer: d.wanted}, nil
		}

		return core.Result{Layer: d.drifted}, nil
	}

	for _, id := range base {
		if id == d.wanted && !d.refused {
			d.refused = true

			return core.Result{}, core.MissingInputError{Layer: id}
		}
	}

	return core.Result{Layer: ir.NodeID{43}}, nil
}

// A rebuild that produces a different layer is not a rebuild.
//
// The recovery path reruns the step that made an unobtainable input and then
// checks it produced the layer that was wanted (I1). If the check is removed the
// scheduler carries on as though the input had been recovered, and the consuming
// step is retried against a base that is still not there - so the same failure
// arrives a second time, wearing the costume of a recovery.
//
// The check was in the catalogue and no test killed the mutant that made it
// vacuous. This is that test. What it watches is the *retry*, because that is
// the only externally visible difference between a rebuild that satisfied the
// check and one that did not.
func TestARebuildThatProducesADifferentLayerIsRefused(t *testing.T) {
	t.Parallel()

	first := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"make", "base"}}}
	second := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"make", "thing"}},
		Inputs: []*ir.Node{first},
	}

	x := &driftingProducer{
		producer: first.ID(),
		consumer: second.ID(),
		wanted:   ir.NodeID{42},
		drifted:  ir.NodeID{99},
	}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "me", IsInvoker: true}},
		Executor: x,
		Blobs:    allBlobs{},
		Record:   &core.Record{},
	}

	_, err := s.Run(t.Context(), &ir.Graph{Root: second})
	if err == nil {
		t.Fatal("a rebuild that produced a different layer was accepted as a" +
			" recovery, so the build reported success over an input that was" +
			" never obtained")
	}

	// The consumer ran once. Running it again would mean the scheduler believed
	// the input had been restored - which is exactly what the removed check
	// would have let it believe.
	if got := x.runs[second.ID()]; got != 1 {
		t.Errorf("the consuming step ran %d time(s), want 1: it was retried"+
			" against a base the rebuild did not produce", got)
	}
}
