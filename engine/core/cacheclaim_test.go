package core_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A locked cache serialises its own users and nobody else.
//
// `--sharing=locked` means one step at a time in that directory, and the guest
// provides it by holding a lock for the step's duration (E427, E432). The guest
// is the wrong place to *wait*: by then the step has a build slot, so a step
// queueing on a cache occupies a slot while doing nothing, and the steps that
// could have used that slot are the ones with no cache at all.
//
// Four steps on one cache and four slots therefore ran one step and idled three
// machines' worth of capacity - **a resource held while waiting for another
// resource**, which is the same shape as a lock convoy and reads, from outside,
// as a build that ignores its parallelism setting (E434).
//
// So the claim is taken before the slot. The order matters and is not
// arbitrary: claim-then-slot cannot deadlock, because a slot is only ever held
// by a step that already has its claims, while slot-then-claim is the
// arrangement where every slot waits for a claim nobody can get.
func TestALockedCacheDoesNotSpendTheBuildsParallelism(t *testing.T) {
	t.Parallel()

	const (
		step    = 40 * time.Millisecond
		users   = 8
		slots   = 2
		repeats = 6
	)

	// Measured as *when the step needing no cache starts*, and repeated.
	//
	// Peak concurrency recovers by itself - the queued steps finish and the free
	// ones run then - so a build that wasted every slot for a whole step reaches
	// the same peak a moment later, and the sweep proved it: swapping the two
	// acquisitions left a peak-based assertion green (E434).
	//
	// Repeated because the losing arrangement loses a *race*, not an ordering.
	// Eight steps queue on one cache with two slots: claiming first, at most one
	// of them ever reaches the semaphore, so a slot is always free and the
	// answer is not a race at all. Taking the slot first, the free step must win
	// one of the two slots against eight competitors - which it sometimes does,
	// which is exactly why once is not an experiment.
	// The bar is measured, not written down.
	//
	// It was `step*5/2` in wall-clock, which passed alone and failed inside the
	// whole-package run at 114ms against a 100ms bar: under load *a wall-clock
	// threshold measures the machine*, and a test that fails when its neighbours
	// are busy reports something nobody asked about (E473).
	//
	// So the same graph is timed with one cache user, where nothing queues and
	// the answer is one base step. Twice, taking the slower: the load that
	// matters is whatever the machine is doing *during* the run, and one
	// baseline taken before a quiet moment is no baseline at all.
	base := max(freeStartsAfter(t, 1, slots, step), freeStartsAfter(t, 1, slots, step))

	// One base step, then everything is ready at once. Anything past another
	// step and a half is a slot spent waiting rather than working - the same
	// distance as the fixed bar, now relative to what this machine manages
	// uncontended.
	bar := base + step*3/2

	for range repeats {
		if at := freeStartsAfter(t, users, slots, step); at > bar {
			t.Fatalf("the step needing no cache started %v in, and %v uncontended"+
				"\n  %d steps queueing on one cache are holding slots while they"+
				" wait, and this one could not get in", at, base, users)
		}
	}
}

// freeStartsAfter times how long a build takes to reach the step that needs no
// cache, with `users` steps contending for one locked cache.
//
// Extracted so the contended run and its own baseline are the *same* code: a
// baseline measured by a second, differently-written build would be a comparison
// between two programs rather than between two arrangements.
func freeStartsAfter(t *testing.T, users, slots int, step time.Duration) time.Duration {
	t.Helper()

	var (
		mu      sync.Mutex
		freeAt  time.Duration
		started = time.Now()
	)

	e := watchExec{func(n *ir.Node) {
		// The base image and the merge have no arguments at all, so this asks
		// rather than indexes.
		if len(n.Op.Args) > 0 && n.Op.Args[0] == "free" {
			mu.Lock()
			freeAt = time.Since(started)
			mu.Unlock()
		}

		time.Sleep(step)
	}}

	s := &core.Scheduler{
		Workers:     []core.Worker{{ID: "w", IsInvoker: true}},
		Executor:    e,
		Blobs:       allBlobs{},
		Parallelism: slots,
	}

	_, err := s.Run(context.Background(), cacheFan(t, users, 1))
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	return freeAt
}

// Two steps never hold one locked cache at the same time.
//
// The guard the slot ordering must not lose. It is asserted at the scheduler
// rather than only in the guest because the scheduler is now the thing that
// decides - and a mechanism that stops waiting in the right place, and stops
// excluding as well, has traded a correctness property for a latency one.
func TestOneLockedCacheAdmitsOneStepAtATime(t *testing.T) {
	t.Parallel()

	var (
		mu     sync.Mutex
		inside int
		peak   int
	)

	e := watchExec{func(*ir.Node) {
		mu.Lock()
		inside++
		if inside > peak {
			peak = inside
		}
		mu.Unlock()

		time.Sleep(30 * time.Millisecond)

		mu.Lock()
		inside--
		mu.Unlock()
	}}

	s := &core.Scheduler{
		Workers:     []core.Worker{{ID: "w", IsInvoker: true}},
		Executor:    e,
		Blobs:       allBlobs{},
		Parallelism: 8,
	}

	_, err := s.Run(context.Background(), cacheFan(t, 4, 0))
	if err != nil {
		t.Fatal(err)
	}

	if peak > 1 {
		t.Errorf("%d steps were inside one --sharing=locked cache at once", peak)
	}
}

// A shared cache is not serialised at all.
//
// `shared` says several steps may use the directory at once. A scheduler that
// claimed it anyway would be providing `locked` under both names - the defect
// E427 recorded, moved one layer up.
func TestASharedCacheIsNotSerialisedByTheScheduler(t *testing.T) {
	t.Parallel()

	e := &slowExec{d: 60 * time.Millisecond}

	g := cacheFanWith(t, 4, 0, ir.Mount{Target: "/c", ID: "npm"})

	s := &core.Scheduler{
		Workers:     []core.Worker{{ID: "w", IsInvoker: true}},
		Executor:    e,
		Blobs:       allBlobs{},
		Parallelism: 4,
	}

	_, err := s.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	if peak := e.peak.Load(); peak < 2 {
		t.Errorf("at most %d steps shared a --sharing=shared cache; the scheduler"+
			" is serialising a mode that asked not to be", peak)
	}
}

// watchExec runs a function for every step and always succeeds.
type watchExec struct{ during func(*ir.Node) }

func (e watchExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	e.during(n)

	return core.Result{Layer: n.ID(), Captured: true}, nil
}

// cacheFan is `users` steps sharing one locked cache plus `free` steps needing
// nothing, all independent of each other.
func cacheFan(t *testing.T, users, free int) *ir.Graph {
	t.Helper()

	return cacheFanWith(t, users, free, ir.Mount{Target: "/c", ID: "cargo", Exclusive: true})
}

func cacheFanWith(t *testing.T, users, free int, m ir.Mount) *ir.Graph {
	t.Helper()

	base := &ir.Node{
		Op:   ir.Op{Kind: ir.OpImage, Args: []string{testImage}},
		Meta: ir.Meta{Source: at(1)},
	}

	leaves := make([]*ir.Node, 0, users+free)

	for i := range users {
		op := ir.Op{Kind: ir.OpExec, Args: []string{"user", string(rune('a' + i))}}
		op.Mounts = []ir.Mount{m}

		leaves = append(leaves, &ir.Node{
			Op: op, Inputs: []*ir.Node{base},
			Meta: ir.Meta{Source: "Earthfile:" + string(rune('2'+i))},
		})
	}

	for i := range free {
		leaves = append(leaves, &ir.Node{
			Op:     ir.Op{Kind: ir.OpExec, Args: []string{"free", string(rune('a' + i))}},
			Inputs: []*ir.Node{base},
			Meta:   ir.Meta{Source: "Earthfile:" + string(rune('6'+i))},
		})
	}

	return &ir.Graph{Root: &ir.Node{
		Op: ir.Op{Kind: ir.OpMerge}, Inputs: leaves, Meta: ir.Meta{Source: at(9)},
	}}
}

// Claims are taken in a fixed order, deduplicated, and only for locked caches.
//
// Asserted directly rather than by provoking a deadlock: the sort is what stops
// two steps naming `a` and `b` in opposite orders from waiting for each other
// for ever, and a race not observed is not a race disproved - E427 deleted this
// exact sort in the guest and fifty racing goroutines failed to notice.
func TestTheOrderClaimsAreTakenIn(t *testing.T) {
	t.Parallel()

	got := core.ClaimOrder([]ir.Mount{
		{ID: "m2", Exclusive: true},
		{ID: "cargo", Exclusive: true},
		{ID: "m2", Exclusive: true},
		{ID: "npm"},                                // shared: several at once
		{Target: "/s", Ephemeral: true},            // private: nothing shared
		{ID: "tok", Secret: true, Exclusive: true}, // staged per step
	})

	want := []string{"cargo", "m2"}
	if !slices.Equal(got, want) {
		t.Errorf("claims %v, want %v", got, want)
	}

	rev := core.ClaimOrder([]ir.Mount{{ID: "m2", Exclusive: true}, {ID: "cargo", Exclusive: true}})
	if !slices.Equal(rev, want) {
		t.Errorf("the same caches named in the other order claim as %v"+
			"\n  two steps would take them in opposite orders and wait for each"+
			" other for ever", rev)
	}
}
