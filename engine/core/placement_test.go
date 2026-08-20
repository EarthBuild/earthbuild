package core_test

import (
	"context"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// placingExec records which worker each step was given.
type placingExec struct {
	mu sync.Mutex
	on map[string]string // step arg -> worker id
}

func (e *placingExec) Run(_ context.Context, n *ir.Node, w core.Worker, _ []ir.NodeID, _ [][]ir.NodeID) (core.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.on == nil {
		e.on = map[string]string{}
	}

	e.on[n.Op.Args[0]] = w.ID

	return core.Result{Layer: n.ID(), Captured: true}, nil
}

func (e *placingExec) where(arg string) string {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.on[arg]
}

// Placement chooses between workers, and nothing had ever given it a choice.
//
// The stage table calls S1 **real** - *"placement, eligibility, L1/L2 lookup,
// profiles"* - and every scheduler in this repository, in production and in
// every test, is constructed with exactly one worker:
//
//	Workers: []core.Worker{localWorker(o.Platform)}
//	Workers: []core.Worker{{ID: "w", IsInvoker: true}}
//
// So `place` has never had to filter, never had to compare loads, and never had
// to break a tie. It is implemented, keyed on by the scheduler, and untested in
// the only situation it exists for - which is the shape flattening was in until
// E49, the view was in until E114, and Κ₂ was in until E125. **Three of the six
// stage rows have now turned out to describe a mechanism nothing exercised.**
//
// A worker is a place a step can run; the scheduler does not care whether it is
// reached over a wire. So this needs no transport, and there was never a reason
// to wait for one.
func TestPlacementChoosesBetweenWorkers(t *testing.T) {
	t.Parallel()

	linux := ir.Platform{OS: testOS, Arch: testArch}

	t.Run("a step goes where it is eligible", func(t *testing.T) {
		t.Parallel()

		e := &placingExec{}

		run(t, e, []core.Worker{
			{ID: testHostClass, Platform: ir.Platform{OS: testOtherOS, Arch: testArch}, IsInvoker: true},
			{ID: testOS, Platform: linux},
		}, &ir.Node{
			Op:       ir.Op{Kind: ir.OpExec, Args: []string{"only-linux"}},
			Platform: linux,
		})

		if got := e.where("only-linux"); got != testOS {
			t.Errorf("a linux/arm64 step ran on %q", got)
		}
	})

	// The rule C.3 states and `eligibleFor` implements: a delegate is not the
	// invoker and must refuse rather than execute. With one worker in the list
	// this could never be wrong, because the only worker was the invoker.
	t.Run("a host step goes to the invoker even when it is busier", func(t *testing.T) {
		t.Parallel()

		e := &placingExec{}

		run(t, e, []core.Worker{
			{ID: "delegate"},
			{ID: "invoker", IsInvoker: true},
		}, &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{"locally"}}})

		if got := e.where("locally"); got != "invoker" {
			t.Errorf("a LOCALLY step ran on %q, which is not the invoking machine", got)
		}
	})

	// Ties break by worker ID, so the choice does not depend on slice order.
	// The comment in `place` says exactly this and nothing checked it: a sort
	// that fell back to slice order would pass every test in the repository.
	t.Run("a tie is broken the same way whatever order the workers arrive in", func(t *testing.T) {
		t.Parallel()

		forward := &placingExec{}
		run(t, forward, []core.Worker{{ID: testFirstKey, IsInvoker: true}, {ID: "zzz", IsInvoker: true}},
			&ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"tied"}}})

		backward := &placingExec{}
		run(t, backward, []core.Worker{{ID: "zzz", IsInvoker: true}, {ID: testFirstKey, IsInvoker: true}},
			&ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"tied"}}})

		if forward.where("tied") != backward.where("tied") {
			t.Errorf("the same build placed a step on %q and then on %q, so placement"+
				" depends on the order the workers were listed in",
				forward.where("tied"), backward.where("tied"))
		}

		if got := forward.where("tied"); got != testFirstKey {
			t.Errorf("the tie broke to %q, not the lowest id", got)
		}
	})

	// And the refusal, when nothing can take the step. `ErrNoEligibleWorker`'s
	// own comment says "no eligible worker" alone is *"true and unusable"*.
	t.Run("no eligible worker names what was wanted", func(t *testing.T) {
		t.Parallel()

		s := &core.Scheduler{
			Workers:  []core.Worker{{ID: testHostClass, Platform: ir.Platform{OS: testOtherOS, Arch: testArch}}},
			Executor: &placingExec{},
			Cache:    newMemCache(),
			Blobs:    allBlobs{},
			Writer:   testStep,
			Record:   &core.Record{},
		}

		n := &ir.Node{
			Op:       ir.Op{Kind: ir.OpExec, Args: []string{"nowhere"}},
			Platform: linux,
		}

		_, err := s.Run(context.Background(), &ir.Graph{Root: n})
		if err == nil {
			t.Fatal("a step no worker can run was scheduled anyway")
		}

		// The platforms, not the worker ids: what a reader needs is what the
		// step wanted and what is on offer, and an id is a name only the fleet
		// knows.
		for _, want := range []string{"linux/arm64", "darwin/arm64"} {
			if !contains(err.Error(), want) {
				t.Errorf("the refusal does not mention %q, so a reader cannot tell"+
					" what the step wanted from what the workers offer:\n%s", want, err)
			}
		}
	})

	// And with several workers it names all of them. With one, "this build has
	// darwin/arm64" is complete by accident; with three it is a summary, and a
	// summary that names one of three sends a reader to add a worker they
	// already have.
	t.Run("several ineligible workers are all named", func(t *testing.T) {
		t.Parallel()

		s := &core.Scheduler{
			Workers: []core.Worker{
				{ID: testHostClass, Platform: ir.Platform{OS: testOtherOS, Arch: testArch}},
				{ID: "pi", Platform: ir.Platform{OS: testOS, Arch: "arm"}},
				{ID: "win", Platform: ir.Platform{OS: "windows", Arch: testArch2}},
			},
			Executor: &placingExec{},
			Cache:    newMemCache(),
			Blobs:    allBlobs{},
			Writer:   testStep,
			Record:   &core.Record{},
		}

		n := &ir.Node{
			Op:       ir.Op{Kind: ir.OpExec, Args: []string{"nowhere"}},
			Platform: linux,
		}

		_, err := s.Run(context.Background(), &ir.Graph{Root: n})
		if err == nil {
			t.Fatal("a step no worker can run was scheduled anyway")
		}

		for _, want := range []string{"darwin/arm64", "linux/arm", "windows/amd64"} {
			if !contains(err.Error(), want) {
				t.Errorf("the refusal does not name the %s worker, so a reader"+
					" cannot tell which of their machines is missing:\n%s", want, err)
			}
		}
	})
}

// Independent steps spread across workers.
//
// The heart of `place`, and the half a single-worker list can never reach:
// *"among the eligible, least-loaded wins"*. Placement happens up-front, in
// order, incrementing a load count as it goes - so two independent steps over
// two identical workers land on one each, and an implementation that ignored
// load entirely would put both on whichever sorted first and pass every other
// test in this file.
//
// Deterministic despite the scheduler running steps concurrently, because
// *placement* is not concurrent: it is a pure function of the graph, decided
// before anything executes. That is what makes this assertable at all rather
// than a flake waiting to happen.
func TestIndependentStepsSpreadAcrossWorkers(t *testing.T) {
	t.Parallel()

	e := &placingExec{}

	// Two independent inputs under a merge, so both are ready at once and
	// neither waits for the other.
	root := &ir.Node{
		Op: ir.Op{Kind: ir.OpMerge, Args: []string{"both"}},
		Inputs: []*ir.Node{
			{Op: ir.Op{Kind: ir.OpExec, Args: []string{"first"}}},
			{Op: ir.Op{Kind: ir.OpExec, Args: []string{"second"}}},
		},
	}

	run(t, e, []core.Worker{{ID: "a", IsInvoker: true}, {ID: "b", IsInvoker: true}}, root)

	first, second := e.where("first"), e.where("second")
	if first == "" || second == "" {
		t.Fatalf("a step was never placed: first=%q second=%q", first, second)
	}

	if first == second {
		t.Errorf("two independent steps both went to %q, so placement is not"+
			" load-aware and a fleet would idle every worker but one", first)
	}
}

func run(t *testing.T, e core.Executor, workers []core.Worker, root *ir.Node) {
	t.Helper()

	s := &core.Scheduler{
		Workers: workers, Executor: e, Cache: newMemCache(), Blobs: allBlobs{},
		Writer: testStep, Record: &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: root})
	if err != nil {
		t.Fatal(err)
	}
}

func contains(hay, needle string) bool {
	return len(needle) > 0 && len(hay) >= len(needle) &&
		(hay == needle || indexOf(hay, needle) >= 0)
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}

	return -1
}
