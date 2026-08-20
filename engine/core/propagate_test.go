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

// A failure stops what stood on it, and stops the build starting anything new.
//
// Two different claims, and only the first is about correctness. A step that
// failed produced no layer, so running its dependent would build on nothing -
// that must never happen. Whether an *independent* branch keeps going is a
// policy, and the scheduler states it: "Cancelled on the first failure, so work
// already started can stop rather than finishing a build that has already
// lost."
//
// So the invariant is **no new work starts after a failure**, not "no
// independent work finishes": a branch that had already completed is not
// undone, and a fast enough one may well complete before the failure lands.
// That is why the seed is fixed - `sim.Executor` replays the same world from
// the same seed, so "the sibling was still running when the failure arrived" is
// a reproducible fact rather than a race.
//
// Untested until now, and the tool for it was sitting unused: `FailNodes` has
// been on `sim.Executor` since it was written, documented as existing "so
// failure paths - retry, propagation, WAIT/END - are reachable without a real
// executor", and **nothing ever set it**. The failure tests hand-roll an
// executor that fails *every* node, which cannot express this graph at all -
// with everything failing there is no surviving branch to make a claim about.
// E154's shape, found by asking which fields are read and never assigned.
func TestAFailureStopsItsDependentsAndStartsNothingNew(t *testing.T) {
	t.Parallel()

	base := &ir.Node{
		Op:   ir.Op{Kind: ir.OpImage, Args: []string{testImage}},
		Meta: ir.Meta{Source: at(1)},
	}

	doomed := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"doomed"}},
		Inputs: []*ir.Node{base},
		Meta:   ir.Meta{Source: at(2)},
	}

	// Stands on the failure. Running it would build on a layer that does not
	// exist, so this one is not a policy.
	downstream := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"downstream"}},
		Inputs: []*ir.Node{doomed},
		Meta:   ir.Meta{Source: at(3)},
	}

	// Shares only the base.
	sibling := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"sibling"}},
		Inputs: []*ir.Node{base},
		Meta:   ir.Meta{Source: at(4)},
	}

	root := &ir.Node{
		Op:     ir.Op{Kind: ir.OpMerge},
		Inputs: []*ir.Node{downstream, sibling},
		Meta:   ir.Meta{Source: at(9)},
	}

	// A gated executor rather than the simulator's seeded durations.
	//
	// This read `sim.Executor{Seed: 1, Sleep: true}` and relied on the sibling
	// still running when the failure landed - which is a fact about the seed and
	// the node identities, not about the scheduler. Adding a field to `ir.Op`
	// changed every identity, changed every drawn duration, and the test failed
	// having tested nothing different (E193).
	//
	// Now the overlap is built rather than drawn: the sibling blocks until it is
	// released or cancelled, so "was it still running" is not a question about
	// timing.
	e := &gatedExec{
		fail:    doomed.ID(),
		gate:    sibling.ID(),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}

	defer close(e.release)

	sched := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: e,
		Blobs:    allBlobs{},
	}

	_, err := sched.Run(context.Background(), &ir.Graph{Root: root})
	if err == nil {
		t.Fatal("a build with a failing step reported success")
	}

	if !strings.Contains(err.Error(), at(2)) {
		t.Errorf("the failure does not name the step that failed:\n%s", err)
	}

	ran := map[ir.NodeID]bool{}
	for _, id := range e.log() {
		ran[id] = true
	}

	if ran[downstream.ID()] {
		t.Error("a step ran on top of one that failed, so it stood on no layer at all")
	}

	// The policy, pinned rather than assumed. If this starts failing, the
	// engine has changed from "stop the build" to "finish what can be
	// finished" - a defensible choice, and one that changes what a failed
	// build leaves behind in the cache, so it should be a decision rather than
	// a drift.
	if !e.cancelled.Load() {
		t.Error("an independent branch was not cancelled when the build failed;" +
			"\n  the scheduler cancels on first failure, and a branch still running is" +
			"\n  work a lost build is paying for")
	}

	if ran[sibling.ID()] {
		t.Error("the independent branch ran to completion after the build had already failed")
	}
}

// gatedExec fails one node, holds another open, and records whether the hold was
// cancelled.
//
// Deterministic where a seeded simulator is not: the overlap this test needs is
// constructed rather than drawn from a distribution that any change to a node's
// identity redraws.
type gatedExec struct {
	fail    ir.NodeID
	gate    ir.NodeID
	started chan struct{}
	release chan struct{}

	cancelled atomic.Bool

	mu   sync.Mutex
	seen []ir.NodeID
}

func (g *gatedExec) Run(
	ctx context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	if n.ID() == g.gate {
		close(g.started)

		select {
		case <-ctx.Done():
			g.cancelled.Store(true)

			return core.Result{}, ctx.Err()
		case <-g.release:
		}
	}

	g.mu.Lock()
	g.seen = append(g.seen, n.ID())
	g.mu.Unlock()

	exit := 0
	if n.ID() == g.fail {
		// The failure waits for the branch it is supposed to interrupt.
		//
		// Otherwise the overlap is a fact about which node the scheduler reached
		// first, which every change to `ir.Op` redraws - E193's finding, and it
		// recurred here after the gate was added: the sibling stopped being
		// *cancelled* and started never running at all, because the failure had
		// already landed by the time its turn came.
		//
		// With a deadline, because a scheduler that runs these serially would
		// otherwise deadlock rather than fail, and a hung test says nothing.
		select {
		case <-g.started:
		case <-time.After(5 * time.Second):
		}

		exit = 3
	}

	return core.Result{Layer: n.ID(), Exit: exit, Captured: true}, nil
}

func (g *gatedExec) log() []ir.NodeID {
	g.mu.Lock()
	defer g.mu.Unlock()

	return append([]ir.NodeID(nil), g.seen...)
}

// A step still waiting its turn does not get one.
//
// The test above pins the *cancellation* - work already running is stopped -
// and mutating `cancel()` away is what proves it. It does not touch the other
// half: a step that has not started yet checks for a failure before it begins,
// and with a worker per CPU nothing ever waits long enough to reach that check.
//
// Removing `cancel()` fails the test above and leaves this one green; removing
// the `stop` guard does the reverse. Two mechanisms, two tests - which is only
// obvious in hindsight, and was found by mutating the wrong one first and
// watching nothing happen.
func TestAQueuedStepDoesNotStartAfterAFailure(t *testing.T) {
	t.Parallel()

	base := &ir.Node{
		Op:   ir.Op{Kind: ir.OpImage, Args: []string{testImage}},
		Meta: ir.Meta{Source: at(1)},
	}

	const width = 6

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

	// Deaf to cancellation on purpose. `sim.Executor` returns `ctx.Err()` the
	// moment it is called, so with it the queued steps stop for that reason and
	// the guard under test never speaks - removing the guard left the test
	// green, which is how this was found. A real executor is deaf for a while:
	// it is inside a syscall, or a container runtime that will notice
	// eventually, and "eventually" is long enough to start a step that should
	// never have begun.
	// **Every** leaf fails, not the first one written down. With a semaphore
	// the order goroutines acquire it in is not the order they were started
	// in, so "leaves[0] fails" left the count anywhere between one and six -
	// which passed on macOS and failed on Linux, which is the definition of a
	// test that was measuring the scheduler's luck.
	//
	// With all of them failing the count is exact: whichever acquires the
	// semaphore first runs and fails, and every other one finds `stop` set
	// before it starts. One, always.
	fail := map[ir.NodeID]int{}
	for _, l := range leaves {
		fail[l.ID()] = 1
	}

	e := &deafExec{fail: fail}

	// One at a time, which is the whole point: the rest of the leaves are ready
	// and waiting behind the one that fails, so they reach the check instead of
	// being started before it matters.
	s := &core.Scheduler{
		Workers:     []core.Worker{{ID: "w", IsInvoker: true}},
		Parallelism: 1,
		Executor:    e,
		Blobs:       allBlobs{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: root})
	if err == nil {
		t.Fatal("a build with a failing step reported success")
	}

	ran := 0

	for _, id := range e.log() {
		for _, l := range leaves {
			if id == l.ID() {
				ran++
			}
		}
	}

	if ran != 1 {
		t.Errorf("%d of %d leaves ran; exactly one should, because the rest were"+
			" queued behind it when it failed", ran, width)
	}
}

// deafExec runs every step it is handed and never looks at the context.
//
// The scheduler's own guard is the only thing that can stop it, which is
// exactly what makes it useful here.
type deafExec struct {
	mu   sync.Mutex
	seen []ir.NodeID
	fail map[ir.NodeID]int
}

func (d *deafExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	d.mu.Lock()
	d.seen = append(d.seen, n.ID())
	d.mu.Unlock()

	return core.Result{Layer: n.ID(), Exit: d.fail[n.ID()], Captured: true}, nil
}

func (d *deafExec) log() []ir.NodeID {
	d.mu.Lock()
	defer d.mu.Unlock()

	return append([]ir.NodeID(nil), d.seen...)
}
