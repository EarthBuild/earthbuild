package fleet_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The driver tells a worker what the step read last time.
//
// The thing that makes a fragment askable-for. This engine records what a step
// looked at (§3.4) and Κ₂ turns it into a prediction of what it will look at
// again - which is exactly the list a worker needs to fetch part of a base
// rather than all of it (E287).
//
// `Hints.ReadsPredicted` has been on the wire since C.3 and carried nothing.
func TestTheDriverSendsWhatAStepReadLastTime(t *testing.T) {
	t.Parallel()

	var seen []fleet.Assignment

	f := &fleet.InProcess{}

	f.AddWorker(func(_ context.Context, a fleet.Assignment) (fleet.Reply, error) {
		seen = append(seen, a)

		return fleet.Reply{Version: fleet.Version, Layer: ir.NodeID{2}}, nil
	})

	d := &fleet.Delegating{
		Local: &countingLocal{},
		Fleet: f,
		Predict: func(*ir.Node) []string {
			return []string{"usr/bin/cc", "usr/lib/libc.so"}
		},
	}

	_, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(seen) != 1 {
		t.Fatalf("%d assignment(s)", len(seen))
	}

	want := []string{"usr/bin/cc", "usr/lib/libc.so"}
	if !slices.Equal(seen[0].Hints.ReadsPredicted, want) {
		t.Errorf("the step was told %v, want %v"+
			"\n  a worker cannot ask for part of a base it has not been told"+
			" about", seen[0].Hints.ReadsPredicted, want)
	}
}

// A step nobody has seen before carries no prediction.
//
// Not an empty list dressed up as knowledge: a worker told "read nothing" would
// fetch a fragment of nothing and fault on every file. Absence has to mean "I do
// not know", and the whole layer is what not knowing costs.
func TestAStepNobodyHasSeenCarriesNoPrediction(t *testing.T) {
	t.Parallel()

	var seen []fleet.Assignment

	f := &fleet.InProcess{}

	f.AddWorker(func(_ context.Context, a fleet.Assignment) (fleet.Reply, error) {
		seen = append(seen, a)

		return fleet.Reply{Version: fleet.Version}, nil
	})

	d := &fleet.Delegating{
		Local:   &countingLocal{},
		Fleet:   f,
		Predict: func(*ir.Node) []string { return nil },
	}

	_, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(seen[0].Hints.ReadsPredicted) != 0 {
		t.Errorf("an unpredicted step was told %v", seen[0].Hints.ReadsPredicted)
	}
}

// A prediction too large to be worth sending is not sent.
//
// A fragment costs its manifest, which is about a hundred bytes an entry - so a
// prediction naming most of a base asks for nearly the whole thing *and* pays
// for the proof. Past some size the honest answer is "fetch the layer", and
// saying nothing is how this protocol says that.
//
// The cap is a judgement and is one line to change; what matters is that there
// is one, because a read set is a step's own business and a step that reads a
// hundred thousand files is not hypothetical.
func TestAPredictionTooLargeToBeWorthSendingIsNotSent(t *testing.T) {
	t.Parallel()

	var seen []fleet.Assignment

	f := &fleet.InProcess{}

	f.AddWorker(func(_ context.Context, a fleet.Assignment) (fleet.Reply, error) {
		seen = append(seen, a)

		return fleet.Reply{Version: fleet.Version}, nil
	})

	huge := make([]string, fleet.MaxPredicted+1)
	for i := range huge {
		huge[i] = fmt.Sprintf("usr/lib/lib%d.so", i)
	}

	d := &fleet.Delegating{
		Local:   &countingLocal{},
		Fleet:   f,
		Predict: func(*ir.Node) []string { return huge },
	}

	_, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := len(seen[0].Hints.ReadsPredicted); got != 0 {
		t.Errorf("a prediction of %d paths was sent as %d"+
			"\n  past a certain size a fragment costs more than the layer",
			len(huge), got)
	}
}

// The prediction changes nothing about the answer.
//
// I5, asserted rather than promised: the same step, once with a prediction and
// once without, produces the same result. A hint that could change what a step
// produces would be a hint that has to be trusted, and nothing in this protocol
// is.
func TestAPredictionDoesNotChangeTheResult(t *testing.T) {
	t.Parallel()

	run := func(predict func(*ir.Node) []string) core.Result {
		f := &fleet.InProcess{}

		f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
			return fleet.Reply{
				Version: fleet.Version,
				Layer:   ir.NodeID{7}, Content: ir.NodeID{8},
			}, nil
		})

		d := &fleet.Delegating{Local: &countingLocal{}, Fleet: f, Predict: predict}

		res, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}

		return res
	}

	with := run(func(*ir.Node) []string { return []string{"usr/bin/cc"} })
	without := run(nil)

	if with.Layer != without.Layer || with.Content != without.Content {
		t.Errorf("a hint changed the result: %v against %v", with, without)
	}
}

// A worker hands the prediction to whatever materialises the base.
//
// The last hop. The driver puts a step's read set in the assignment (E287); the
// executor is what assembles a base and only the node reaches it - so the worker
// copies one to the other, and it is safe because `Meta` is not in the identity
// (E301).
func TestAWorkerHandsThePredictionToTheExecutor(t *testing.T) {
	t.Parallel()

	var seen []string

	run := fleet.Runner(&noting{saw: &seen}, core.Worker{ID: "w1"})

	_, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Hints:   fleet.Hints{ReadsPredicted: []string{"usr/bin/cc"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(seen) != 1 || seen[0] != "usr/bin/cc" {
		t.Errorf("the executor was told %v"+
			"\n  it is what assembles the base, and it cannot prime a fragment"+
			" of a read set nobody gave it", seen)
	}
}

// noting records what the node it was handed says it will read.
type noting struct{ saw *[]string }

func (n *noting) Run(
	_ context.Context, node *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	*n.saw = node.Meta.ReadsPredicted

	return core.Result{Layer: ir.NodeID{1}}, nil
}
