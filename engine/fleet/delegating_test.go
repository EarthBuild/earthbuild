package fleet_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// countingLocal records how often the local executor was used.
//
// Guarded: an executor is called from as many goroutines as the scheduler has
// steps in flight, so a fake with a bare counter reports a race for a property
// every real executor has to have anyway.
type countingLocal struct {
	mu   sync.Mutex
	runs int
	err  error
}

func (c *countingLocal) Run(
	context.Context, *ir.Node, core.Worker, []ir.NodeID, [][]ir.NodeID,
) (core.Result, error) {
	c.mu.Lock()
	c.runs++
	err := c.err
	c.mu.Unlock()

	return core.Result{Layer: ir.NodeID{1}}, err
}

func delegable() *ir.Node {
	return &ir.Node{
		Op:   ir.Op{Kind: ir.OpExec, Args: []string{"make"}},
		Meta: ir.Meta{Source: "Earthfile:3"},
	}
}

// A step placed on a worker goes to the worker; one placed here stays here.
func TestPlacementDecidesWhereAStepRuns(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		worker    core.Worker
		wantLocal int
	}{
		{"the invoker", core.Worker{ID: "me", IsInvoker: true}, 1},
		{"a worker", core.Worker{ID: "w1"}, 0},
	} {
		local := &countingLocal{}
		f := &fleet.InProcess{}

		f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
			return fleet.Reply{Version: fleet.Version, Layer: ir.NodeID{2}}, nil
		})

		d := &fleet.Delegating{Local: local, Fleet: f}

		_, err := d.Run(context.Background(), delegable(), tc.worker, nil, nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		if local.runs != tc.wantLocal {
			t.Errorf("%s: the local executor ran %d times, want %d",
				tc.name, local.runs, tc.wantLocal)
		}
	}
}

// Refusing to delegate is not refusing to build.
//
// A step carrying a secret, a cache mount or a `host` op cannot be expressed in
// an assignment (E230). The work is perfectly possible - it is only *this
// machine* that can do it - so the answer is to run it here.
//
// The same applies to every way the fleet can fail to take a step: nobody
// available, everybody vanished, a worker that refused. A build that failed
// because a worker rebooted would be worse than a slow one (I11).
func TestAStepThatCannotBeDelegatedIsStillBuilt(t *testing.T) {
	t.Parallel()

	gone := func(context.Context, fleet.Assignment) (fleet.Reply, error) {
		return fleet.Reply{}, fleet.ErrWorkerGone
	}

	refuses := func(context.Context, fleet.Assignment) (fleet.Reply, error) {
		return fleet.Reply{
			Version: fleet.Version,
			Refused: "this engine does not implement that construct",
		}, nil
	}

	for _, tc := range []struct {
		name   string
		node   *ir.Node
		worker func(*fleet.InProcess)
	}{
		{
			name: "a host step",
			node: &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{"make"}}},
		},
		{
			name: "a step with a cache mount",
			node: &ir.Node{Op: ir.Op{
				Kind: ir.OpExec, Args: []string{"make"},
				Mounts: []ir.Mount{{ID: "m", Target: "/c"}},
			}},
		},
		{
			name:   "an empty fleet",
			node:   delegable(),
			worker: func(*fleet.InProcess) {},
		},
		{
			name:   "a fleet that vanished",
			node:   delegable(),
			worker: func(f *fleet.InProcess) { f.AddWorker(gone) },
		},
		{
			name:   "a worker that refused",
			node:   delegable(),
			worker: func(f *fleet.InProcess) { f.AddWorker(refuses) },
		},
	} {
		local := &countingLocal{}
		f := &fleet.InProcess{}

		if tc.worker != nil {
			tc.worker(f)
		} else {
			f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
				return fleet.Reply{Version: fleet.Version}, nil
			})
		}

		d := &fleet.Delegating{Local: local, Fleet: f}

		_, err := d.Run(context.Background(), tc.node, core.Worker{ID: "w1"}, nil, nil)
		if err != nil {
			t.Errorf("%s: the build failed: %v", tc.name, err)

			continue
		}

		if local.runs != 1 {
			t.Errorf("%s: the step was not built here (%d local runs); the work"+
				" is possible and only this machine can do it",
				tc.name, local.runs)
		}
	}
}

// A worker cannot assert its own credibility.
//
// The observation arrives from a machine this one did not write (A5), and
// `Observed` is set from what the observation *contains* rather than from
// anything the worker says about it - exactly as it is for a local step. A reply
// naming nothing yields an unobserved result, which the driver's existing rules
// then refuse to key on, because an empty observation agrees with every base.
func TestAWorkersEmptyObservationIsNotTreatedAsAnObservation(t *testing.T) {
	t.Parallel()

	f := &fleet.InProcess{}
	f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
		return fleet.Reply{Version: fleet.Version, Layer: ir.NodeID{5}}, nil
	})

	d := &fleet.Delegating{Local: &countingLocal{}, Fleet: f}

	got, err := d.Run(context.Background(), delegable(), core.Worker{ID: "w1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got.Observed {
		t.Error("a reply that named nothing produced an observed result;" +
			" an empty observation agrees with every base (I3)")
	}

	// And one that says something is carried faithfully.
	f2 := &fleet.InProcess{}
	f2.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
		return fleet.Reply{
			Version: fleet.Version, Layer: ir.NodeID{5},
			Observation: fleet.Observation{
				Reads: map[string]ir.NodeID{"/etc/passwd": {6}},
			},
		}, nil
	})

	d2 := &fleet.Delegating{Local: &countingLocal{}, Fleet: f2}

	got, err = d2.Run(context.Background(), delegable(), core.Worker{ID: "w1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !got.Observed || got.Observation.Reads["/etc/passwd"] != (ir.NodeID{6}) {
		t.Errorf("the worker's observation did not survive: %+v", got.Observation)
	}
}

// A local executor's own failure is a build failure.
//
// The fallbacks above are about the *fleet* not taking a step. Once the work is
// happening here, an error is an error - swallowing it would turn a broken build
// into a silent one.
func TestALocalFailureIsStillAFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("the sandbox would not start")
	local := &countingLocal{err: boom}

	d := &fleet.Delegating{Local: local, Fleet: &fleet.InProcess{}}

	_, err := d.Run(context.Background(), delegable(), core.Worker{ID: "w1"}, nil, nil)
	if !errors.Is(err, boom) {
		t.Errorf("a local failure became %v", err)
	}
}
