package fleet

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step whose inputs cost more to ship than the step is worth stays here.
//
// **The lever the second attempt did not have.** Pricing (E317) decides *which*
// worker a step goes to; nothing decided whether to delegate it at all, so a
// fleet shipped a base worth three hundred steps of compute to save one step -
// and came out slower than one machine on a graph that was embarrassingly
// parallel.
//
// The rule is the honest floor rather than a scheduler: if this machine already
// holds everything the step reads, and moving those bytes to a worker would take
// longer than simply running it, run it. No forecast of the whole build, no
// model of what else might want the slot - just the one comparison that cannot
// be wrong in the direction that matters.
func TestAStepNotWorthShippingRunsHere(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	fleet := &countingTransport{}

	d := &Delegating{
		Local: local(core.Result{Layer: ir.NodeID{2}}),
		Fleet: fleet,
		Store: &mapStore{has: map[ir.NodeID]bool{base: true}},
		Sizes: func(ir.NodeID) int64 { return 100 << 20 },
	}

	// A megabyte a second, steps worth a second: this base is a hundred steps.
	d.rate.Observe(1<<20, 1000, 1000)

	_, err := d.Run(t.Context(), node(), core.Worker{ID: "w"}, []ir.NodeID{base}, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if fleet.count() != 0 {
		t.Errorf("a base worth a hundred steps was offered to the fleet %d"+
			" time(s)\n  shipping it to save one step is how a distributed"+
			" build comes out slower than one machine", fleet.count())
	}
}

// A step worth shipping is still shipped.
//
// The other half, and the one that would quietly turn a fleet off: a rule that
// kept everything would pass the test above and every build would run on one
// machine.
func TestAStepWorthShippingStillGoes(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	fleet := &countingTransport{}

	d := &Delegating{
		Local: local(core.Result{Layer: ir.NodeID{2}}),
		Fleet: fleet,
		Store: &mapStore{has: map[ir.NodeID]bool{base: true}},
		Sizes: func(ir.NodeID) int64 { return 1 << 10 },
	}

	d.rate.Observe(1<<20, 1000, 1000)

	_, err := d.Run(t.Context(), node(), core.Worker{ID: "w"}, []ir.NodeID{base}, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if fleet.count() != 1 {
		t.Errorf("a kilobyte base was offered to the fleet %d time(s), want 1"+
			"\n  a rule that keeps everything is a fleet switched off",
			fleet.count())
	}
}

// A step this machine cannot run without fetching is shipped anyway.
//
// The comparison only holds when running here is *free of transfer*. If the
// driver would have to bring the base back from whichever worker made it, both
// choices move the bytes and keeping the step buys nothing but a busy driver.
func TestAStepWhoseInputsAreElsewhereIsStillShipped(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	fleet := &countingTransport{}

	d := &Delegating{
		Local: local(core.Result{Layer: ir.NodeID{2}}),
		Fleet: fleet,
		Store: &mapStore{}, // this machine holds nothing
		Sizes: func(ir.NodeID) int64 { return 100 << 20 },
	}

	d.rate.Observe(1<<20, 1000, 1000)

	_, err := d.Run(t.Context(), node(), core.Worker{ID: "w"}, []ir.NodeID{base}, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if fleet.count() != 1 {
		t.Errorf("a step whose base is not here was offered to the fleet %d"+
			" time(s), want 1\n  keeping it buys a fetch either way", fleet.count())
	}
}

// countingTransport says yes and remembers how often it was asked.
type countingTransport struct {
	mu      sync.Mutex
	asked   int
	reply   Reply
	delay   time.Duration
	workers int
}

func (c *countingTransport) Assign(context.Context, Assignment) (Reply, error) {
	c.mu.Lock()
	c.asked++
	c.mu.Unlock()

	time.Sleep(c.delay)

	if c.reply.Version != 0 {
		return c.reply, nil
	}

	return Reply{Version: Version, Layer: ir.NodeID{2}}, nil
}

func (c *countingTransport) Workers() int { return max(c.workers, 1) }

func (c *countingTransport) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.asked
}

// runsHere is an executor that succeeds without doing anything.
type runsHere struct{ res core.Result }

func (r runsHere) Run(
	context.Context, *ir.Node, core.Worker, []ir.NodeID, [][]ir.NodeID,
) (core.Result, error) {
	return r.res, nil
}

func local(res core.Result) core.Executor { return runsHere{res: res} }

// mapStore is a Keeper that only has to answer Has.
type mapStore struct{ has map[ir.NodeID]bool }

func (m *mapStore) Has(id ir.NodeID) bool { return m.has[id] }

func (m *mapStore) Put(io.Reader) (ir.NodeID, int64, error) {
	return ir.NodeID{}, 0, errNotAStore
}

var errNotAStore = errors.New("this store only answers Has")

func node() *ir.Node {
	return &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"make"}}}
}

// A step kept here is counted once.
//
// The account is what every measurement in this project is read off, and a step
// counted twice makes a build that declined to delegate look like one that ran
// twice as much work. Written because the first version of `noteKept` did
// exactly that, and nothing else would have noticed.
func TestAStepKeptHereIsCountedOnce(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	d := &Delegating{
		Local: local(core.Result{Layer: ir.NodeID{2}}),
		Fleet: &countingTransport{},
		Store: &mapStore{has: map[ir.NodeID]bool{base: true}},
		Sizes: func(ir.NodeID) int64 { return 100 << 20 },
	}

	d.rate.Observe(1<<20, 1000, 1000)

	_, err := d.Run(t.Context(), node(), core.Worker{ID: "w"}, []ir.NodeID{base}, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if got := d.Spend().Local; got != 1 {
		t.Errorf("one step kept here counted as %d", got)
	}
}

// A driver learns what its fleet costs from the fleet.
//
// **Without this the rule above never fires.** `Slots` falls back to the
// constant when nothing has been measured, the constant is below the threshold,
// and every step is delegated - so a driver that never fed its own rate would
// behave exactly like one with no rule at all, and no test of the rule in
// isolation would notice.
//
// *Failure class: a mechanism that is not running and one that found nothing
// produce the same output.* Met for the fourth time in this project, and the
// reason this test exists rather than a comment saying the wiring is there.
func TestADriverLearnsWhatItsFleetCosts(t *testing.T) {
	t.Parallel()

	// The layer the first step produces, which the second stands on and which
	// this machine gets back the ordinary way.
	base, made := ir.NodeID{1}, ir.NodeID{2}

	fleet := &countingTransport{reply: Reply{
		Version: Version, Layer: ir.NodeID{2}, Bytes: 100 << 20,
		FetchedBytes: 1 << 20, FetchMillis: 1000, DurationMillis: 1000,
	}}

	d := &Delegating{
		Local: local(core.Result{Layer: ir.NodeID{9}}),
		Fleet: fleet,
		Store: &mapStore{has: map[ir.NodeID]bool{base: true, made: true}},
		Sizes: func(ir.NodeID) int64 { return 0 },
	}

	// The first step goes out and teaches the driver what a transfer costs.
	_, err := d.Run(t.Context(), node(), core.Worker{ID: "w"}, []ir.NodeID{base}, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if fleet.count() != 1 {
		t.Fatalf("the first step was offered %d time(s), want 1", fleet.count())
	}

	// The second stands on what the first produced - a hundred megabytes, now
	// known because a worker said so - and is not worth shipping.
	_, err = d.Run(t.Context(), node(), core.Worker{ID: "w"},
		[]ir.NodeID{made}, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if fleet.count() != 1 {
		t.Errorf("a hundred-megabyte base was offered to the fleet again" +
			"\n  the driver measured the cost of a transfer and did not use it")
	}
}

// One step goes out to find out what the fleet costs; the rest wait for it.
//
// **The cold start, and it is not a corner case - it is every build's first
// wave.** E318's rule needs a measured fleet, and a build that launches six
// steps at once decides all six before any reply exists. Measured over a real
// LAN: 31.2 MiB moved for 183ms of total compute, 23s of overhead, every step
// delegated, and the rule that exists to prevent exactly that never fired
// (E319).
//
// So the first delegable step with inputs worth pricing goes out alone and the
// others wait for what it learns. Bounded, because a fleet that never answers
// must not stall a build - and only for steps that would be expensive, because
// a cheap step has nothing to gain by waiting.
func TestOneStepTeachesTheRestWhatTheFleetCosts(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	fleet := &countingTransport{
		delay: 50 * time.Millisecond,
		reply: Reply{
			Version: Version, Layer: ir.NodeID{2},
			FetchedBytes: 1 << 20, FetchMillis: 1000, DurationMillis: 1,
		},
	}

	d := &Delegating{
		Local: local(core.Result{Layer: ir.NodeID{9}}),
		Fleet: fleet,
		Store: &mapStore{has: map[ir.NodeID]bool{base: true}},
		Sizes: func(ir.NodeID) int64 { return 100 << 20 },
	}

	var wg sync.WaitGroup

	for range 6 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := d.Run(t.Context(), node(), core.Worker{ID: "w"},
				[]ir.NodeID{base}, nil)
			if err != nil {
				t.Errorf("%v", err)
			}
		}()
	}

	wg.Wait()

	if fleet.count() != 1 {
		t.Errorf("%d of 6 steps were offered to the fleet, want 1"+
			"\n  a hundred-megabyte base against one-millisecond steps is the"+
			" arrangement a fleet must refuse, and a whole wave deciding"+
			" before the first reply cannot refuse anything (E319)",
			fleet.count())
	}
}

// No more steps wait for the price than could act on it.
//
// **The pilot gate became the dominant cost.** Measured with eight steps: the
// first goes out alone and the other seven wait ~600ms for it, which was most of
// a 1.4s build (E321). Most of them were never going to be kept - this machine
// has room for two - so they waited for an answer that could not change what
// happened to them.
//
// So the gate holds at most as many steps as this machine could actually keep.
// The rest go, because waiting to find out whether to keep a step you have
// nowhere to put is delay with no possible benefit.
func TestNoMoreStepsWaitForThePriceThanCouldActOnIt(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	// A fleet with room to spare, so this test exercises the gate and not the
	// saturation rule that sits in front of it (E320).
	fleet := &blockingTransport{
		blockAt: 0, // hold every assignment, so the gate cannot open
		workers: 8,
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		reply: Reply{
			Version: Version, Layer: ir.NodeID{2}, Capacity: 8,
			DurationMillis: 1,
		},
	}

	d := &Delegating{
		Local: local(core.Result{Layer: ir.NodeID{9}}),
		Fleet: fleet,
		Store: &mapStore{has: map[ir.NodeID]bool{base: true}},
		Sizes: func(ir.NodeID) int64 { return 100 << 20 },
		Room:  1,
	}

	var wg sync.WaitGroup

	for range 4 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := d.Run(t.Context(), node(), core.Worker{ID: "w"},
				[]ir.NodeID{base}, nil)
			if err != nil {
				t.Errorf("%v", err)
			}
		}()
	}

	// The pilot, plus the two that had nowhere to wait for: three assignments
	// while the pilot is still held open. Without the bound only the pilot ever
	// arrives and this times out.
	for fleet.count() < 3 && t.Context().Err() == nil {
		time.Sleep(time.Millisecond)
	}

	// Sampled **while the pilot is still held**. After it is released everything
	// proceeds and the count says nothing; the first version of this assertion
	// was made at the end and measured the timeout rather than the mechanism.
	during := fleet.count()

	close(fleet.release)
	wg.Wait()

	if during != 3 {
		t.Errorf("%d step(s) reached the fleet while the price was unknown,"+
			" want 3\n  a step this machine has no room for gains nothing by"+
			" waiting to hear whether it should be kept (E321)", during)
	}
}

// Two steps finishing at once do not close the same gate twice.
//
// **`close of closed channel`, in a real run.** `learned` checked whether the
// gate was already shut and then shut it, which two goroutines can both pass -
// and a build of twelve steps on a fleet of two workers found it within seconds.
//
// *Failure class: TOCTOU on a check-then-act.* The check reads as a guard and is
// not one; only `sync.Once` is.
func TestTheGateIsClosedOnceHoweverManyStepsFinishTogether(t *testing.T) {
	t.Parallel()

	d := &Delegating{}

	var wg sync.WaitGroup

	for range 64 {
		wg.Add(1)

		go func() { defer wg.Done(); d.learned() }()
	}

	wg.Wait()

	select {
	case <-d.taught():
	default:
		t.Error("the gate never opened")
	}
}

// A step with a prediction is shipped even though its base is large.
//
// **The decision, not the arithmetic.** `Typical` can be right and unused: what
// matters is that `keepHere` prices a predicted step by what steps actually
// move. Measured at four workers, a 16 MB base moved 1.1 MiB in total and every
// decision was made against 16 MB a step, so the driver kept work it should have
// shipped (E326).
//
// The driver has to be **busy** for the price to decide anything: an idle
// machine finishes a step in one wave and beats the fleet at any transfer cost,
// correctly. The first version of this test had one idle step and asserted the
// opposite.
func TestAStepWithAPredictionIsShippedDespiteALargeBase(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	fleet := &countingTransport{workers: 1, reply: Reply{
		Version: Version, Layer: ir.NodeID{2}, Capacity: 1,
		DurationMillis: 1,
	}}

	held := make(chan struct{})
	ran := make(chan struct{}, 4)

	d := &Delegating{
		Local:   blockingLocal{held: held, ran: ran},
		Fleet:   fleet,
		Store:   &mapStore{has: map[ir.NodeID]bool{base: true}},
		Sizes:   func(ir.NodeID) int64 { return 100 << 20 },
		Predict: func(*ir.Node) []string { return []string{"usr/lib/a.so"} },
		Room:    1,
	}

	// A megabyte a second, steps of a second, and steps that moved a megabyte -
	// a fragment of that base, not the whole of it.
	d.rate.Observe(1<<20, 1000, 1000)

	run := func() {
		_, err := d.Run(t.Context(), node(), core.Worker{ID: "w"},
			[]ir.NodeID{base}, nil)
		if err != nil {
			t.Errorf("%v", err)
		}
	}

	var wg sync.WaitGroup

	// One step occupies this machine's only slot.
	wg.Add(1)

	go func() { defer wg.Done(); run() }()

	<-ran

	// The next belongs with the fleet - one wave there against two here - and
	// would not if a fragment were priced as a whole base.
	run()

	close(held)
	wg.Wait()

	if fleet.count() != 1 {
		t.Errorf("a step reading one file of a 100 MB base was kept behind a" +
			" busy machine\n  priced as though the whole base would cross," +
			" which is two orders of magnitude out (E326)")
	}
}

// Shipping to a machine that already holds the inputs is free.
//
// **A term the model did not have.** `keepHere` prices every delegation as
// though the base had to cross, and once a worker has fetched it, sending that
// worker another step on the same base moves nothing at all. The driver went on
// charging for a transfer that had already happened.
//
// It matters most where a fleet is most useful: a fan-out over one base, where
// exactly one machine pays and every step after that is free to place. Charging
// each of them makes an expensive base look like a reason to keep sixteen steps
// on one machine (E344).
//
// The driver knows without being told: holders are recorded from replies, and a
// holder that is not this machine is a worker that has the bytes.
func TestShippingToAMachineThatHasTheInputsIsFree(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	fleet := &countingTransport{workers: 1, reply: Reply{
		Version: Version, Layer: ir.NodeID{2}, Capacity: 1,
		DurationMillis: 1, HeldAt: "w@host:9",
	}}

	held := make(chan struct{})
	ran := make(chan struct{}, 8)

	d := &Delegating{
		// Blocks the **first** step only: this machine must be busy for the
		// comparison to decide anything, and a local executor that blocked
		// every step would deadlock on the one the test expects to be kept.
		Local: &blockingOnce{held: held, ran: ran},
		Fleet: fleet,
		Store: &mapStore{has: map[ir.NodeID]bool{base: true}},
		Sizes: func(ir.NodeID) int64 { return 100 << 20 },
		Room:  1,
	}

	// A measured fleet on which that base is worth a great many steps.
	d.rate.Observe(1<<20, 1000, 1000)

	run := func() {
		_, err := d.Run(t.Context(), node(), core.Worker{ID: "w"},
			[]ir.NodeID{base}, nil)
		if err != nil {
			t.Errorf("%v", err)
		}
	}

	var wg sync.WaitGroup

	// One step occupies this machine, so the comparison is not a walkover.
	wg.Add(1)

	go func() { defer wg.Done(); run() }()

	<-ran

	// Nobody holds the base yet: a hundred megabytes to ship against one queued
	// step here, so it stays.
	run()

	if fleet.count() != 0 {
		t.Fatalf("a hundred-megabyte base was shipped to a fleet that does not"+
			" have it, %d time(s)", fleet.count())
	}

	// Now a worker holds it - recorded from a reply - and the same comparison
	// should send work there, because sending it costs nothing.
	d.held.also([]ir.NodeID{base}, "w@host:9")

	run()

	if fleet.count() != 1 {
		t.Errorf("a step was kept behind a busy machine rather than sent to a" +
			" worker that already holds its inputs (E344)")
	}

	close(held)
	wg.Wait()
}

// blockingOnce holds the first step it is given and lets the rest through.
type blockingOnce struct {
	held chan struct{}
	ran  chan struct{}

	mu sync.Mutex
	n  int
}

func (b *blockingOnce) Run(
	ctx context.Context, _ *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	b.mu.Lock()
	b.n++
	first := b.n == 1
	b.mu.Unlock()

	if first {
		b.ran <- struct{}{}

		select {
		case <-b.held:
		case <-ctx.Done():
		}
	}

	return core.Result{Layer: ir.NodeID{9}}, nil
}

// A saturated fleet sends work back to the machine that asked.
//
// **The driver ran nothing in a level-shaped build**, even against a single
// worker saturated by a level of four (E346). It could not: keeping required
// already holding the step's inputs, and from the second level of any graph
// those live on whichever worker made them.
//
// With the fleet two waves deep and this machine idle, fetching the inputs here
// is a step finished sooner - and `bringBack` is how a driver has got a worker's
// layer since E274, so nothing new has to be built to run it.
func TestASaturatedFleetSendsWorkBackToTheDriver(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{3}

	// A fleet with one slot, four steps deep, and nothing this machine holds.
	fleet := &countingTransport{workers: 1, reply: Reply{
		Version: Version, Layer: ir.NodeID{4}, Capacity: 1,
		DurationMillis: 1, HeldAt: "w@host:9",
	}}

	brought := 0

	d := &Delegating{
		Local: local(core.Result{Layer: ir.NodeID{9}}),
		Fleet: fleet,
		Store: &mapStore{has: map[ir.NodeID]bool{}},
		Sizes: func(ir.NodeID) int64 { return 4096 },
		Room:  4,
		Peers: func(string) (Source, error) {
			brought++

			return &emptySourceFor{}, nil
		},
	}

	// A measured fleet, and a worker that holds the base already.
	d.rate.Observe(1<<20, 10, 1000)
	d.rate.Observe(1<<10, 10, 1000)
	d.held.also([]ir.NodeID{base}, "w@host:9")

	// Fill the fleet: four steps in flight against its one slot.
	d.flightForTest(4)

	_, err := d.Run(t.Context(), node(), core.Worker{ID: "w"},
		[]ir.NodeID{base}, nil)

	// **The failure is the evidence.** This driver has a peer dialler that
	// supplies nothing, so a step kept here tries to bring its inputs back and
	// cannot - which is exactly what a kept step does, and a delegated one would
	// have succeeded. Asserting the error is asserting the decision.
	if !errors.Is(err, core.ErrInputMissing) {
		t.Fatalf("want a step kept here and unable to fetch its inputs, got %v",
			err)
	}

	if fleet.count() != 0 {
		t.Errorf("a step went to a fleet four waves deep while this machine sat" +
			" idle\n  keeping required already holding the inputs, which after" +
			" the first level is never true (E347)")
	}

	if brought == 0 {
		t.Error("nothing was dialled to bring the inputs back")
	}
}

// emptySourceFor answers nothing, which is enough: what is asserted is where
// the step went, not that the fetch succeeded.
type emptySourceFor struct{}

func (e *emptySourceFor) Name() string { return "peer" }

func (e *emptySourceFor) Fetch(
	context.Context, []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	return nil, nil
}
