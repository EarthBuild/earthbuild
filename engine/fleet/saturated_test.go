package fleet

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step does not queue behind a full fleet when this machine is free.
//
// **The whole-build gap E318 left open, stated in the plan and now measured.**
// Every rule so far judges one step in isolation: is *this* step worth shipping?
// Six cheap steps each answer yes, go to a worker with room for one, and five of
// them queue - while the machine that asked sits idle holding every input.
//
// A queue is not free and it is not visible in any per-step comparison. The
// driver is a machine too, and the only party that knows both how many steps are
// in flight and how much room the fleet said it had.
//
// The same shape as the rules around it: it fires only when running here costs
// no transfer, so it can never trade a queue for a fetch.
func TestAStepDoesNotQueueBehindAFullFleet(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	// A worker with room for one, held inside Assign until every step has had
	// its chance to decide.
	fleet := &blockingTransport{
		blockAt: 1,
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		reply: Reply{
			Version: Version, Layer: ir.NodeID{2}, Capacity: 1,
			DurationMillis: 1,
		},
	}

	d := &Delegating{
		Local: local(core.Result{Layer: ir.NodeID{9}}),
		Fleet: fleet,
		Store: &mapStore{has: map[ir.NodeID]bool{base: true}},
		Sizes: func(ir.NodeID) int64 { return 4096 },
	}

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()

		_, err := d.Run(t.Context(), node(), core.Worker{ID: "w"},
			[]ir.NodeID{base}, nil)
		if err != nil {
			t.Errorf("%v", err)
		}
	}()

	// The first step is now inside the transport, occupying the fleet's only
	// slot - and stays there until released, so the fleet is genuinely full
	// while the rest decide.
	<-fleet.entered

	// Timed, because the mechanism this needs is not only *what* is decided but
	// *when*. Deciding after the pilot returns gives the same split and takes
	// `PilotWait` to get there, and a test that only counted the split let that
	// deletion survive mutation (E322).
	began := time.Now()

	// **Synchronously**, so the fleet is still occupied when each one chooses.
	// Started as goroutines and released immediately, the first assignment
	// returns before the others have decided anything and the test measures
	// nothing - which is how the first version of it passed a broken engine.
	for range 5 {
		_, err := d.Run(t.Context(), node(), core.Worker{ID: "w"},
			[]ir.NodeID{base}, nil)
		if err != nil {
			t.Fatalf("%v", err)
		}
	}

	close(fleet.release)
	wg.Wait()

	if took := time.Since(began); took > time.Second {
		t.Errorf("five steps took %v to decide they belonged here, want prompt"+
			"\n  a fleet that is already fuller than this machine is a reason"+
			" to run here whatever the transfer costs, so there is nothing to"+
			" wait for (E322)", took)
	}

	if got := fleet.count(); got != 1 {
		t.Errorf("%d step(s) were offered to a fleet with room for one, want 1"+
			"\n  five steps queued while the machine holding every input was"+
			" idle (E320)", got)
	}

	if got := d.Spend().Local; got != 5 {
		t.Errorf("%d step(s) ran here, want 5", got)
	}
}

// blockingTransport holds the first assignment until it is released.
type blockingTransport struct {
	mu      sync.Mutex
	asked   int
	reply   Reply
	entered chan struct{}
	release chan struct{}
	// blockAt is which assignment is held open, one-based.
	blockAt int
	// workers is how many this fleet claims, one when unset.
	workers int
}

func (b *blockingTransport) Assign(context.Context, Assignment) (Reply, error) {
	b.mu.Lock()
	b.asked++
	n := b.asked
	b.mu.Unlock()

	// blockAt 0 holds *every* assignment. Necessary when what is being tested
	// is the pilot gate: the pilot is not reliably the first to reach here -
	// steps that bypass the gate can overtake it - so blocking "the first one"
	// blocks the wrong step and the gate opens while the test believes it shut.
	if b.blockAt == 0 || n == b.blockAt {
		if n == max(b.blockAt, 1) {
			b.entered <- struct{}{}
		}

		<-b.release
	}

	return b.reply, nil
}

func (b *blockingTransport) Workers() int { return max(b.workers, 1) }

func (b *blockingTransport) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.asked
}

// A worker with room for four is not treated as a worker with room for one.
//
// The other side of E320, and the one that would quietly switch a fleet off: a
// driver that never learnt how much room a worker admitted to would size every
// fleet at one slot per machine and keep everything else. Half the mechanism -
// the half that keeps steps - would still pass every test.
//
// Capacity arrives on the reply and is the worker's own statement about itself,
// which is the only party that knows.
func TestAWorkersAdmittedRoomWidensTheFleet(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	fleet := &blockingTransport{
		blockAt: 2, // the second assignment is held open, not the first
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		reply: Reply{
			Version: Version, Layer: ir.NodeID{2}, Capacity: 4,
			DurationMillis: 1,
		},
	}

	d := &Delegating{
		Local: local(core.Result{Layer: ir.NodeID{9}}),
		Fleet: fleet,
		Store: &mapStore{has: map[ir.NodeID]bool{base: true}},
		Sizes: func(ir.NodeID) int64 { return 4096 },
	}

	run := func() {
		t.Helper()

		_, err := d.Run(t.Context(), node(), core.Worker{ID: "w"},
			[]ir.NodeID{base}, nil)
		if err != nil {
			t.Errorf("%v", err)
		}
	}

	// One step out and back, which is where the driver hears "room for four".
	run()

	var wg sync.WaitGroup

	wg.Add(1)

	go func() { defer wg.Done(); run() }()

	<-fleet.entered

	// One slot of four is busy, so these belong with the fleet.
	run()
	run()

	close(fleet.release)
	wg.Wait()

	if got := fleet.count(); got != 4 {
		t.Errorf("%d of 4 steps were offered to a fleet with room for four"+
			"\n  a fleet sized at one slot per machine keeps work it should"+
			" ship (E320)", got)
	}
}

// A step is not kept here when this machine is as full as the fleet.
//
// **The mirror of E320, found by measuring it.** Keeping a step because the
// fleet is busy is right only while this machine can actually take it: eight
// steps against a fleet with two slots and a driver with two produced two
// delegated and six queued *here*, which is the same queue in a different place.
//
// When both are full the step goes to the fleet. Not a coin toss: the driver is
// the machine every other decision in this build also runs through, and a fleet
// is the elastic side of the pair - a worker that queues is a worker that starts
// the moment it can, while a driver that queues delays everything it is also
// doing (E321).
func TestAStepIsNotKeptWhenThisMachineIsFullToo(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	fleet := &blockingTransport{
		blockAt: 1,
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		reply: Reply{
			Version: Version, Layer: ir.NodeID{2}, Capacity: 1,
			DurationMillis: 1,
		},
	}

	// A local executor that blocks, so this machine can be full on purpose.
	held := make(chan struct{})
	ran := make(chan struct{}, 8)

	d := &Delegating{
		Local: blockingLocal{held: held, ran: ran},
		Fleet: fleet,
		Store: &mapStore{has: map[ir.NodeID]bool{base: true}},
		Sizes: func(ir.NodeID) int64 { return 4096 },
		Room:  1,
	}

	// A fleet already measured, and a fast one: this test is about capacity, and
	// an unmeasured rate would send the third step to wait on a pilot the test
	// is deliberately holding open (E319).
	d.rate.Observe(1<<20, 1, 1000)

	run := func() {
		_, err := d.Run(t.Context(), node(), core.Worker{ID: "w"},
			[]ir.NodeID{base}, nil)
		if err != nil {
			t.Errorf("%v", err)
		}
	}

	var wg sync.WaitGroup

	// One occupies the fleet's only slot.
	wg.Add(1)

	go func() { defer wg.Done(); run() }()

	<-fleet.entered

	// One is kept, and occupies this machine's only slot.
	wg.Add(1)

	go func() { defer wg.Done(); run() }()

	<-ran

	// Both full now: this one belongs with the fleet, which is the side that
	// starts the moment it can.
	wg.Add(1)

	go func() { defer wg.Done(); run() }()

	for fleet.count() < 2 && t.Context().Err() == nil {
		time.Sleep(time.Millisecond)
	}

	close(held)
	close(fleet.release)
	wg.Wait()

	if got := fleet.count(); got != 2 {
		t.Errorf("%d step(s) went to the fleet, want 2"+
			"\n  a driver that keeps work it cannot run has moved the queue,"+
			" not removed it (E321)", got)
	}
}

// blockingLocal is an executor that reports it started and then waits.
type blockingLocal struct {
	held chan struct{}
	ran  chan struct{}
}

func (b blockingLocal) Run(
	ctx context.Context, _ *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	b.ran <- struct{}{}

	select {
	case <-b.held:
	case <-ctx.Done():
	}

	return core.Result{Layer: ir.NodeID{9}}, nil
}
