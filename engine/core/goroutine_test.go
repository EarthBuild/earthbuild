package core_test

import (
	"context"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The scheduler's fan-out is joined, on both the way it can end.
//
// Test-plan b5 at the layer that has the goroutines: `Run` starts one per ready
// step and the build ends when they are all accounted for. The success path is
// the easy half - `wg.Wait()` is right there - and the failure path is the one
// worth pinning, because it cancels a context, abandons the queue, and then
// runs handlers during unwind (E37). Every one of those is a place to leave
// something running.
//
// No VM and no network, so this runs everywhere the unit tests do, which is
// where a leak wants catching: the sandbox suite is opt-in and a leak that only
// shows there is one nobody sees until they look.
func TestTheSchedulerLeavesNoGoroutinesBehind(t *testing.T) { //nolint:paralleltest // counts goroutines
	// Sequential of necessity: the count is of the *process*, so any other
	// test running beside this one is a goroutine it would blame the
	// scheduler for. Go runs the sequential tests before the parallel ones,
	// which is exactly the isolation this needs.

	// Not parallel: it counts goroutines, and a test running beside it is
	// exactly the noise the count cannot tell from a leak.
	build := func(t *testing.T, fails bool) {
		t.Helper()

		base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}, Platform: amd64}
		prev := base

		// Wide enough that the fan-out is real rather than a single step in a
		// straight line.
		for i := range 8 {
			cmd := testLeaf
			if fails && i == 4 {
				cmd = testFailure
			}

			prev = &ir.Node{
				Op:       ir.Op{Kind: ir.OpExec, Args: []string{cmd, string(rune('a' + i))}},
				Platform: amd64,
				Inputs:   []*ir.Node{prev},
				Meta:     ir.Meta{Source: "Earthfile:" + string(rune('a'+i))},
			}
		}

		handler := &ir.Node{
			Op:        ir.Op{Kind: ir.OpExec, Args: []string{testCleanup}},
			Platform:  amd64,
			Inputs:    []*ir.Node{prev},
			OnFailure: prev,
			Meta:      ir.Meta{Source: "Earthfile:handler"},
		}

		s := newSched(newMemCache(), allBlobs{}, &unwindExec{})

		_, err := s.Run(context.Background(), &ir.Graph{Root: prev, Also: []*ir.Node{handler}})
		if fails && err == nil {
			t.Fatal("the build was supposed to fail")
		}

		if !fails && err != nil {
			t.Fatal(err)
		}
	}

	// A first build so the baseline holds whatever the runtime starts once.
	build(t, false)

	before := settledCount()

	for range 3 {
		build(t, false)
		build(t, true)
	}

	after := settledCount()

	const slack = 3

	if after > before+slack {
		t.Errorf("six builds left %d goroutines behind (%d -> %d)\n%s",
			after-before, before, after, debug.Stack())
	}
}

// settledCount is the goroutine count once it stops moving.
func settledCount() int {
	var (
		prev  = -1
		count int
	)

	for range 50 {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)

		count = runtime.NumGoroutine()
		if count == prev {
			break
		}

		prev = count
	}

	return count
}

var _ = core.Scheduler{}
