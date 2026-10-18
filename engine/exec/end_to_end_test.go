package exec_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestSchedulerDrivesRealProcesses is the S4 claim, end to end: a graph goes
// through the scheduler, over the guest protocol, and comes out as processes
// that actually ran on this machine.
//
// Everything below the scheduler is real here - the wire format, the guest
// server, the exec - which is the difference between this and the simulator
// suite it otherwise resembles.
func TestSchedulerDrivesRealProcesses(t *testing.T) {
	if !needsIsolation(t) {
		return
	}

	t.Parallel()

	e, err := exec.New(&exec.Local{})
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	// A chain, so the scheduler has to order them: each step's input is the one
	// before it.
	var prev *ir.Node

	for _, name := range []string{"a", "b", "c"} {
		n := step(t, name, "true")
		if prev != nil {
			n.Inputs = []*ir.Node{prev}
		}

		prev = n
	}

	cache := memCache{}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: testLocal, IsInvoker: true}},
		Executor: e,
		Cache:    cache,
		Blobs:    allBlobs{},
		Writer:   "test",
		Record:   &core.Record{},
	}

	_, err = s.Run(context.Background(), &ir.Graph{Root: prev})
	if err != nil {
		t.Fatal(err)
	}

	if got := len(s.Record.Steps); got != 3 {
		t.Fatalf("ran %d steps, want 3", got)
	}

	// Nothing may be cached: the local sandbox does not confine, so A3 does not
	// hold and no result it produces is a claim anyone should trust.
	if got := len(cache); got != 0 {
		t.Errorf("unconfined run published %d cache entries, want 0", got)
	}

	for _, r := range s.Record.Steps {
		if r.Outcome != core.OutcomeUncaptured {
			t.Errorf("%s: outcome %v, want uncaptured", r.Meta.Source, r.Outcome)
		}
	}
}

type memCache map[core.Key]core.Entry

func (m memCache) Get(k core.Key) (core.Entry, bool) { e, ok := m[k]; return e, ok }
func (m memCache) Put(k core.Key, e core.Entry)      { m[k] = e }

type allBlobs struct{}

func (allBlobs) Has(ir.NodeID) bool { return true }
