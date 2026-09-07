package core_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// slowExec sleeps, and records how many steps were in flight at once.
type slowExec struct {
	d        time.Duration
	inFlight atomic.Int32
	peak     atomic.Int32

	mu    sync.Mutex
	order []string // completion order, which must not reach any result
}

func (e *slowExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	now := e.inFlight.Add(1)
	for {
		peak := e.peak.Load()
		if now <= peak || e.peak.CompareAndSwap(peak, now) {
			break
		}
	}

	time.Sleep(e.d)
	e.inFlight.Add(-1)

	e.mu.Lock()
	e.order = append(e.order, n.Meta.Source)
	e.mu.Unlock()

	return core.Result{Layer: n.ID(), Captured: true}, nil
}

// fan builds one root over n independent steps: the shape every real build has,
// and the one a serial scheduler wastes.
func fan(width int) (*ir.Graph, []*ir.Node) {
	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}, Meta: ir.Meta{Source: at(1)}}

	leaves := make([]*ir.Node, 0, width)
	for i := range width {
		leaves = append(leaves, &ir.Node{
			Op:     ir.Op{Kind: ir.OpExec, Args: []string{"leaf", string(rune('a' + i))}},
			Inputs: []*ir.Node{base},
			Meta:   ir.Meta{Source: "Earthfile:" + string(rune('2'+i))},
		})
	}

	root := &ir.Node{
		Op: ir.Op{Kind: ir.OpMerge}, Inputs: leaves,
		Meta: ir.Meta{Source: at(9)},
	}

	return &ir.Graph{Root: root}, leaves
}

// Independent steps run at the same time.
//
// The prototype this engine replaces had a correct scheduler and a serial build
// loop, so it produced the right answer at the speed of one core. Wall-clock is
// the only honest test of that.
func TestIndependentStepsRunConcurrently(t *testing.T) {
	t.Parallel()

	g, _ := fan(4)

	e := &slowExec{d: 80 * time.Millisecond}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: e,
		Blobs:    allBlobs{},
	}

	_, err := s.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	// **The clock is gone and the counter stays**, which is the same trade E350
	// records for the fragment-cost test. A "serial is ~480ms, concurrent is
	// ~240ms, so fail over 400ms" bound is a ratio of two clocks: it is a
	// generous margin on an idle laptop and no margin at all on a machine
	// running the rest of this suite, or in a container given two cores. It
	// failed twice under full-suite load and once in Docker while the property
	// it guards held perfectly.
	//
	// The property is "steps are not serialised", and `peak` answers that
	// exactly - it is the number that were running at one moment, counted.
	// Bounded below by 2 rather than by the number of leaves, because how many
	// run at once is the scheduler's business and the machine's; that any two
	// did is the claim.
	if peak := e.peak.Load(); peak < 2 {
		t.Errorf("at most %d step ran at a time; nothing was concurrent", peak)
	}
}

// A step never starts before the steps it depends on have finished.
func TestDependenciesAreStillRespected(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		ended = map[string]bool{}
	)

	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}, Meta: ir.Meta{Source: testBase}}
	mid := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{testMid}},
		Inputs: []*ir.Node{base},
		Meta:   ir.Meta{Source: testMid},
	}
	top := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{testTop}},
		Inputs: []*ir.Node{mid},
		Meta:   ir.Meta{Source: testTop},
	}

	s := &core.Scheduler{
		Workers: []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: orderExec{before: func(src string) {
			mu.Lock()
			defer mu.Unlock()

			switch src {
			case testMid:
				if !ended[testBase] {
					t.Error("mid started before base finished")
				}
			case testTop:
				if !ended[testMid] {
					t.Error("top started before mid finished")
				}
			}
		}, after: func(src string) {
			mu.Lock()
			ended[src] = true
			mu.Unlock()
		}},
		Blobs: allBlobs{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: top})
	if err != nil {
		t.Fatal(err)
	}
}

type orderExec struct{ before, after func(string) }

func (e orderExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	e.before(n.Meta.Source)
	time.Sleep(5 * time.Millisecond)
	e.after(n.Meta.Source)

	return core.Result{Layer: n.ID(), Captured: true}, nil
}

// Green paper (4.10): any legal schedule yields the same artefacts. Concurrency
// is a legal schedule, so a parallel build and a serial one must agree - and the
// *record* must agree too, or every tool that diffs two builds reports noise.
func TestConcurrencyDoesNotReachTheResult(t *testing.T) {
	t.Parallel()

	var first []string

	for run := range 5 {
		g, _ := fan(6)

		s := &core.Scheduler{
			Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
			Executor: &slowExec{d: time.Millisecond},
			Blobs:    allBlobs{},
		}

		_, err := s.Run(context.Background(), g)
		if err != nil {
			t.Fatal(err)
		}

		got := make([]string, 0, len(s.Record.Steps))
		for _, r := range s.Record.Steps {
			got = append(got, r.Meta.Source+"="+r.Layer.String())
		}

		if run == 0 {
			first = got

			continue
		}

		if strings.Join(got, ",") != strings.Join(first, ",") {
			t.Fatalf("run %d recorded a different build:\n%v\n%v", run, first, got)
		}
	}
}

// When two steps fail at once, which failure is reported must not depend on
// which finished first: a build that blames a different command each time is a
// build nobody can act on.
func TestTheReportedFailureIsDeterministic(t *testing.T) {
	t.Parallel()

	var seen string

	for range 5 {
		g, _ := fan(4)

		s := &core.Scheduler{
			Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
			Executor: flakyOrder{},
			Blobs:    allBlobs{},
		}

		_, err := s.Run(context.Background(), g)
		if err == nil {
			t.Fatal("a build with failing steps reported success")
		}

		if seen == "" {
			seen = err.Error()

			continue
		}

		if err.Error() != seen {
			t.Fatalf("two runs blamed different steps:\n%s\n%s", seen, err)
		}
	}
}

// flakyOrder fails every leaf, at randomly varying speeds.
type flakyOrder struct{}

func (flakyOrder) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	if n.Op.Kind != ir.OpExec {
		return core.Result{Layer: n.ID(), Captured: true}, nil
	}

	// Later leaves finish sooner, so completion order is the reverse of graph
	// order - the case where "first to fail" and "first in the Earthfile"
	// disagree.
	//
	// **From the leaf's position, not the length of its name.** This read
	// `len(n.Op.Args[1])`, and that argument is one character - `a`, `b`, `c` -
	// so every leaf slept the same 17ms and completion order was a race. The
	// test was flaky where it meant to be adversarial: it failed when the race
	// happened to expose the engine and passed when it did not, which is the
	// worst of both, because a green run said nothing.
	nth := int(n.Op.Args[1][0] - 'a')

	time.Sleep(time.Duration(20-nth*5) * time.Millisecond)

	return core.Result{Layer: n.ID(), Captured: true, Exit: 1, Output: "failed: " + n.Meta.Source}, nil
}
