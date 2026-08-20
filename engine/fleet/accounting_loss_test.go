package fleet_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Every transfer that happened is accounted for.
//
// A real fan-out showed the holder serving **two** copies of a base while the
// account recorded **one**, which turns the forecast's disagreement (E268) from
// "placement is cheaper than the model" into "the account loses a transfer". The
// difference matters: the first would be a pleasant surprise, the second makes
// every number this project has measured suspect, because the account is the
// instrument.
//
// Two workers, separate stores, the same base, at once - the shape the fleet was
// in when it happened, without the network.
//
// **It passes**, and that is the point of keeping it: the worker's side of the
// accounting is correct, so the transfer that goes missing goes missing on the
// driver's side of the wire. A test that excludes a suspect is worth as much as
// one that convicts, and much more than the paragraph of reasoning it replaces
// (E269).
func TestEveryTransferThatHappenedIsAccountedFor(t *testing.T) {
	t.Parallel()

	body := make([]byte, 32<<10)

	remote := newMapStore()
	id := putBlob(t, remote, body)

	src := &countingSource{LayerSource: &fleet.LayerSource{Label: "holder", Held: remote}}

	a := fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{id},
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		reported int64
	)

	for range 2 {
		run := fleet.Runner(&countingLocal{}, core.Worker{ID: "w"},
			fleet.WithBlobs(newMapStore(), src))

		wg.Add(1)

		go func() {
			defer wg.Done()

			reply, err := run(t.Context(), a)
			if err != nil {
				return
			}

			mu.Lock()
			reported += reply.FetchedBytes
			mu.Unlock()
		}()
	}

	wg.Wait()

	if src.batches != 2 {
		t.Fatalf("two workers with empty stores made %d fetch(es)", src.batches)
	}

	if reported != 2*int64(len(body)) {
		t.Errorf("two transfers happened and %d byte(s) were reported, want %d"+
			"\n  the account is the instrument every measurement in this"+
			" project is taken with", reported, 2*len(body))
	}
}

// failing is an executor that cannot start a step.
type failing struct{ err error }

func (f failing) Run(
	context.Context, *ir.Node, core.Worker, []ir.NodeID, [][]ir.NodeID,
) (core.Result, error) {
	return core.Result{}, f.err
}

// A step that fetched and then could not start still reports what it moved.
//
// The bug behind E268's disagreement, and it is in the engine rather than in the
// model. Every reply path but one carries what the worker had to move; the path
// taken when the *executor* refuses to start returns without it - so a step that
// pulled four hundred megabytes across the network and then failed on a missing
// binary is recorded as having cost nothing.
//
// **The bytes were spent.** They do not become free because the step that needed
// them did not run, and an account that says otherwise makes a fleet look cheaper
// exactly when it is being least useful.
func TestAStepThatFetchedAndThenFailedStillReportsIt(t *testing.T) {
	t.Parallel()

	body := make([]byte, 48<<10)

	remote := newMapStore()
	id := putBlob(t, remote, body)

	run := fleet.Runner(failing{err: errors.New("no such binary")},
		core.Worker{ID: "w1"},
		fleet.WithBlobs(newMapStore(), &fleet.LayerSource{Held: remote}))

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{id},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.Refused == "" {
		t.Fatal("an executor that could not start the step did not refuse")
	}

	if reply.FetchedBytes != int64(len(body)) {
		t.Errorf("reported %d byte(s) moved, want %d"+
			"\n  the transfer happened; the step failing afterwards does not"+
			" make it free", reply.FetchedBytes, len(body))
	}
}

// slow is an executor that takes a known time.
type slow struct{ took time.Duration }

func (s slow) Run(
	context.Context, *ir.Node, core.Worker, []ir.NodeID, [][]ir.NodeID,
) (core.Result, error) {
	time.Sleep(s.took)

	return core.Result{Layer: ir.NodeID{1}}, nil
}

// A worker times the step it ran.
//
// `DurationMillis` was documented as "how long the step took, as the worker
// measured it" and nothing measured it. The first run of a fleet over a real
// network reported **compute 0s, overhead 45s** for seven steps that each took
// two seconds - so the account said "overhead-bound", which sends somebody to
// look at their network when the answer is that the steps take two seconds
// (E276).
//
// The worker is the only party that can time this. The driver's round trip
// includes the queue, the transfer and the network; the difference between that
// and the step itself is exactly what the account calls overhead, and it is
// meaningless if one side of the subtraction is always zero.
func TestAWorkerTimesTheStepItRan(t *testing.T) {
	t.Parallel()

	const took = 120 * time.Millisecond

	run := fleet.Runner(slow{took: took}, core.Worker{ID: "w1"})

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.DurationMillis < took.Milliseconds()/2 {
		t.Errorf("a step that took %v was reported as %dms"+
			"\n  an account whose compute is always zero calls every build"+
			" overhead-bound", took, reply.DurationMillis)
	}

	// And not wildly more: it is the *step*, not the assignment. Counting the
	// wait for a slot or the transfer here would hide them inside compute,
	// which is the one number nobody would then question.
	if reply.DurationMillis > 4*took.Milliseconds() {
		t.Errorf("a step that took %v was reported as %dms; something other"+
			" than the step is being counted as compute", took, reply.DurationMillis)
	}
}
