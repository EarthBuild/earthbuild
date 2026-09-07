package cli

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The build's scheduler gets the fleet the driver found.
//
// `EARTH_FLEET_WORKERS` makes the driver wait for workers, announce them, and
// hand back a `fleet.Delegating` executor whose `Remote()` names them. That
// executor and that worker list reached the scheduler used to answer
// *conditions* - `IF`, `ARG x = $(...)` - and **not the one that runs the
// build**, which built its own with `Workers: []core.Worker{localWorker(...)}`
// and the plain executor.
//
// So a fleet was joined, printed, and never used: every step ran on the invoker
// while workers sat idle, and the only sign was a build that was not faster
// (E500).
//
// Placement is the reason the worker *list* matters as much as the executor: a
// scheduler that does not know a worker exists never places a step on it,
// whatever executor it is given.
func TestTheBuildSchedulesOntoTheFleet(t *testing.T) {
	t.Parallel()

	remote := []core.Worker{{ID: "w1"}, {ID: "w2"}}

	x := &fleet.Delegating{Local: nil, Fleet: &listedFleet{workers: remote}}

	g := &engine{fleetEx: x}

	got, workers := g.scheduling(nil, "linux/arm64")

	if got != core.Executor(x) {
		t.Error("the build was given an executor other than the fleet's, so" +
			" every step runs locally whatever the scheduler places")
	}

	names := map[string]bool{}
	for _, w := range workers {
		names[w.ID] = true
	}

	for _, w := range remote {
		if !names[w.ID] {
			t.Errorf("worker %s was found by the driver and is not in the"+
				" build's worker list, so nothing can be placed on it", w.ID)
		}
	}

	// And this machine is still one of them: the invoker runs steps too, and a
	// build that placed nothing locally would be slower on a one-worker fleet
	// than with no fleet at all.
	var invoker bool

	for _, w := range workers {
		if w.IsInvoker {
			invoker = true
		}
	}

	if !invoker {
		t.Error("the invoker is not in the build's worker list")
	}
}

// With no fleet, the build is local and says nothing about workers.
func TestABuildWithNoFleetSchedulesLocally(t *testing.T) {
	t.Parallel()

	plain := &countingExec{}

	g := &engine{}

	got, workers := g.scheduling(plain, "linux/arm64")

	if got != core.Executor(plain) {
		t.Error("a build with no fleet was given something other than its own executor")
	}

	if len(workers) != 1 || !workers[0].IsInvoker {
		t.Errorf("a local build has %d workers, want one invoker", len(workers))
	}
}

// listedFleet is a fleet whose inventory is written down.
type listedFleet struct{ workers []core.Worker }

func (f *listedFleet) Inventory() []core.Worker { return f.workers }

func (f *listedFleet) Assign(context.Context, fleet.Assignment) (fleet.Reply, error) {
	return fleet.Reply{}, nil
}

func (f *listedFleet) Workers() int { return len(f.workers) }

// countingExec stands in for the local executor.
type countingExec struct{}

func (c *countingExec) Run(
	context.Context, *ir.Node, core.Worker, []ir.NodeID, [][]ir.NodeID,
) (core.Result, error) {
	return core.Result{}, nil
}
