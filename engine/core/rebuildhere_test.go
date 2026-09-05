package core_test

import (
	"context"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ranOn records which worker each step ran on, and reports one layer missing.
//
// The layer is reported missing exactly once, on the step that consumes it, so
// the scheduler is made to do the thing under test and then allowed to finish.
type ranOn struct {
	mu   sync.Mutex
	on   map[ir.NodeID][]string
	miss ir.NodeID
	done bool
}

func (e *ranOn) Run(
	_ context.Context, n *ir.Node, w core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.on == nil {
		e.on = map[ir.NodeID][]string{}
	}

	e.on[n.ID()] = append(e.on[n.ID()], w.ID)

	// One refusal, from the consumer, naming the layer its input produced.
	if !e.done && len(n.Inputs) == 1 && n.Inputs[0].ID() == e.miss {
		e.done = true

		return core.Result{}, core.MissingInputError{Layer: e.miss}
	}

	return core.Result{Layer: n.ID(), Captured: true}, nil
}

// A layer that could not be obtained is rebuilt on the invoker.
//
// **Where it is rebuilt is the whole of the fix.** The layer was unobtainable
// from wherever it was made, so making it there again would make it out of reach
// again - the build would ask a second time, get the same answer, and fail with
// a transfer error rather than the reason (E278).
//
// The invoker is the machine running the build. It is the one place a rebuilt
// layer is certainly reachable from, because it is where the question was asked.
//
// Nothing checked this. The mutation catalogue pins the line, and with the
// invoker replaced by an empty worker every test in the package still passed:
// the layer was rebuilt, the build went green, and the only difference was that
// it had been rebuilt nowhere in particular.
func TestAnUnobtainableLayerIsRebuiltOnTheInvoker(t *testing.T) {
	t.Parallel()

	produced := exec1("produce")(alpine)
	g := &ir.Graph{Root: exec1("consume")(produced)}

	e := &ranOn{miss: produced.ID()}

	s := &core.Scheduler{
		// The invoker second, so a scheduler picking the first eligible worker
		// rather than the invoking one fails this rather than passing by
		// accident.
		Workers: []core.Worker{
			{ID: "elsewhere", Platform: amd64},
			{ID: "here", Platform: amd64, IsInvoker: true},
		},
		Executor: e, Cache: newMemCache(), Blobs: allBlobs{}, Writer: "t",
	}

	_, err := s.Run(context.Background(), g)
	if err != nil {
		t.Fatalf("the build did not survive one missing layer: %v", err)
	}

	on := e.on[produced.ID()]
	if len(on) < 2 {
		t.Fatalf("the producing step ran %d time(s) on %v; the missing layer"+
			" was never rebuilt, so this test measured nothing", len(on), on)
	}

	// The rebuild is the run after the first: the first is the ordinary build
	// of that step, the second is the one this is about.
	if got := on[1]; got != "here" {
		t.Errorf("the layer was rebuilt on %q, want the invoker %q"+
			"\n  it was unobtainable from where it was made, so making it there"+
			" again makes it unobtainable again (E278)", got, "here")
	}
}
