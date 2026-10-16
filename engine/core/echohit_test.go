package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step served from the cache says what it said.
//
// **Without this the mechanism is inert.** A result carries what its step
// printed, and a hit hands that result back - but nobody sees it unless the
// scheduler replays it through the sink a running step's lines go to. That
// replay is the whole of what makes a `$( )` substitution cacheable, and a
// build log complete on a warm build.
func TestAServedStepSaysWhatItSaid(t *testing.T) {
	t.Parallel()

	shared := newMemCache()
	e := &printingExec{out: "three\nfiles\n"}

	var echoed []string

	run := func() {
		s := &core.Scheduler{
			Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
			Executor: e,
			Cache:    shared,
			Blobs:    allBlobs{},
			Writer:   testStep,
			Record:   &core.Record{},
			Echo:     func(_ *ir.Node, out string) { echoed = append(echoed, out) },
		}

		n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"ls"}}}

		if _, err := s.Run(context.Background(), &ir.Graph{Root: n}); err != nil {
			t.Fatal(err)
		}
	}

	run()

	if len(echoed) != 0 {
		t.Errorf("a step that ran had its output replayed as well as printed:"+
			" %q\n  saying it twice is worse than not saying it", echoed)
	}

	if e.runs != 1 {
		t.Fatalf("the first build ran the step %d times", e.runs)
	}

	run()

	if e.runs != 1 {
		t.Fatalf("the second build ran the step again, so nothing was cached")
	}

	if len(echoed) != 1 || echoed[0] != "three\nfiles\n" {
		t.Errorf("a served step replayed %q, want what it printed"+
			"\n  a hit that is silent is how `LET v=$(cmd)` came to evaluate to"+
			"\n  the empty string on every build after the first", echoed)
	}
}

// printingExec runs a step, counts it, and reports what it printed.
type printingExec struct {
	runs int
	out  string
}

func (c *printingExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	c.runs++

	return core.Result{
		Layer: n.ID(), Content: n.ID(), Captured: true,
		Stdout: c.out, StdoutWhole: true,
	}, nil
}
