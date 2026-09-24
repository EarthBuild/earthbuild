package fleet

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

func layerOf(n byte) ir.NodeID { return ir.NodeID{n} }

// A chain costs nothing to move, whatever the fleet's size.
//
// The prediction half of E265, and it is the same code making the decision:
// `Predict` calls `preferFree`, so a change to placement changes the forecast
// automatically. A simulator that modelled placement separately would agree with
// the engine right up until somebody edited one of them, and its agreement would
// then be worth nothing.
func TestAChainIsPredictedToMoveNothing(t *testing.T) {
	t.Parallel()

	const size = 256 << 10

	steps := make([]Step, 0, 4)
	for i := range 4 {
		s := Step{Produces: layerOf(byte(i + 1)), Size: size}
		if i > 0 {
			s.Base = []ir.NodeID{layerOf(byte(i))}
		}

		steps = append(steps, s)
	}

	for _, workers := range []int{1, 2, 8} {
		got := Predict(steps, workers, 1)
		if got.Moved != 0 {
			t.Errorf("%d worker(s): predicted %d byte(s) for a chain"+
				"\n  a chain that stays put moves nothing; one that rotates"+
				" moves its base every step", workers, got.Moved)
		}
	}
}

// A fan-out costs one copy of the base per machine that joins in.
//
// Not per step, which is what it cost before the transfers were serialised
// (E266), and not zero, which is what it would cost if the whole fan-out ran on
// one machine and the fleet were pointless.
func TestAFanOutIsPredictedToCostOneCopyPerMachine(t *testing.T) {
	t.Parallel()

	const size = 256 << 10

	steps := make([]Step, 0, 9)
	steps = append(steps, Step{Produces: layerOf(1), Size: size})

	for i := range 8 {
		steps = append(steps, Step{
			Base:     []ir.NodeID{layerOf(1)},
			Produces: layerOf(byte(i + 2)),
			Size:     size,
		})
	}

	got := Predict(steps, 3, 8)

	if got.Transfers != 2 {
		t.Errorf("predicted %d transfer(s) for an eight-way fan-out over three"+
			" machines\n  the two that did not produce the base each need it"+
			" once: %+v", got.Transfers, got)
	}

	if got.Moved != 2*size {
		t.Errorf("predicted %d byte(s), want %d", got.Moved, 2*size)
	}

	if len(got.Ran) < 2 {
		t.Errorf("predicted the whole fan-out on %d machine(s)", len(got.Ran))
	}
}

// A fleet of one moves nothing, whatever the shape.
//
// The baseline every comparison is against: if a build is not faster than this,
// the fleet is not earning its transfers.
func TestOneMachineIsPredictedToMoveNothing(t *testing.T) {
	t.Parallel()

	steps := []Step{
		{Produces: layerOf(1), Size: 1 << 20},
		{Base: []ir.NodeID{layerOf(1)}, Produces: layerOf(2), Size: 1 << 20},
		{Base: []ir.NodeID{layerOf(1)}, Produces: layerOf(3), Size: 1 << 20},
	}

	got := Predict(steps, 1, 4)
	if got.Moved != 0 || got.Transfers != 0 {
		t.Errorf("one machine was predicted to send itself %d byte(s)", got.Moved)
	}
}

// A base from outside the fleet is counted apart from one between workers.
//
// **This test used to assert it was not counted at all**, on the argument that
// the fleet-internal number is the one placement can do something about. The
// argument is half right and the omission was not survivable: E315 measured a
// build that moved 1.6 MiB and forecast zero.
//
// So both, separately, because they are different levers. Bytes between workers
// come down by placing a step where its inputs already are. Bytes from the
// origin come down by not delegating the step at all - and on a cold fleet they
// are the larger number by far.
func TestABaseFromOutsideTheFleetIsCountedApart(t *testing.T) {
	t.Parallel()

	steps := []Step{{
		Base:     []ir.NodeID{layerOf(9)}, // produced by nobody here
		Produces: layerOf(1),
		Size:     1 << 20,
	}}

	got := PredictWith(steps, 3, 1, map[ir.NodeID]int64{layerOf(9): 1 << 20})

	if got.Transfers != 1 {
		t.Errorf("counted %d transfer(s) for a base no worker produced, want 1",
			got.Transfers)
	}

	if got.FromOrigin != 1<<20 {
		t.Errorf("counted %d byte(s) from outside the fleet, want %d",
			got.FromOrigin, 1<<20)
	}

	if got.Moved-got.FromOrigin != 0 {
		t.Errorf("counted %d byte(s) between workers for a build with one"+
			" step\n  the two costs have different remedies and one number"+
			" hides that", got.Moved-got.FromOrigin)
	}
}

// A base the driver holds is a transfer, and the forecast says so.
//
// **E315 caught the model saying zero for a run that moved 1.6 MiB.** The
// exclusion was deliberate - "a base that came from the driver is not a fleet
// transfer: it is not a cost placement can do anything about" - and it is wrong
// in the way that matters most here.
//
// It *is* a cost, it *is* avoidable (run the step where the bytes already are,
// or do not delegate it at all), and on a cold fleet it is the **dominant**
// term: every worker's first step pulls a base nobody else has. A scheduler
// tuned against a number that omits the largest cost will place work exactly
// where the bytes are worst, which is a fair description of what the second
// attempt at this did.
//
// Sizes come in separately because a layer no step produces has no `Step` to
// carry its size - the seed base is an input to the build, not an output of it.
func TestABaseFromTheDriverCountsAsATransfer(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	steps := []Step{
		{Base: []ir.NodeID{base}, Produces: ir.NodeID{2}, Size: 10},
		{Base: []ir.NodeID{base}, Produces: ir.NodeID{3}, Size: 10},
	}

	got := PredictWith(steps, 1, 1, map[ir.NodeID]int64{base: 1000})

	if got.Transfers != 1 {
		t.Errorf("forecast %d transfer(s) for a cold worker's first base,"+
			" want 1\n  a model that cannot see the largest cost in a build"+
			" places work as though the network were free (E315)",
			got.Transfers)
	}

	if got.Moved != 1000 {
		t.Errorf("forecast %d byte(s) moved, want 1000", got.Moved)
	}
}

// The same base is not counted twice for the same worker.
//
// The half that was already right and must stay right: a worker keeps its store
// between steps, so the second step standing on a base it already pulled costs
// nothing. Counting it again would make a warm fleet look like a cold one and
// send the scheduler spreading work to avoid a transfer that is not there.
func TestABaseAlreadyOnAWorkerIsNotCountedAgain(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{1}

	steps := []Step{
		{Base: []ir.NodeID{base}, Produces: ir.NodeID{2}, Size: 10},
		{Base: []ir.NodeID{base}, Produces: ir.NodeID{3}, Size: 10},
		{Base: []ir.NodeID{base}, Produces: ir.NodeID{4}, Size: 10},
	}

	got := PredictWith(steps, 1, 1, map[ir.NodeID]int64{base: 1000})

	if got.Moved != 1000 {
		t.Errorf("forecast %d byte(s) for three steps on one base, want 1000"+
			"\n  a worker keeps its store between steps", got.Moved)
	}
}

// The model prices a fetch the way the engine does.
//
// `Predict` exists because it *is* the engine's placement, so a change to one
// changes the other. Pricing broke that: the engine began asking `Rate` what a
// base was worth (E317) while the model went on charging a constant, and a
// simulator that disagrees with the thing it simulates is two guesses checking
// each other.
//
// The arrangement matters. A base **nobody** holds is pulled by whoever runs the
// step whatever it costs - the first attempt at this test asserted otherwise and
// failed for a good reason. Price decides between machines that differ in what
// they hold: here one worker made the base and is busy, and the question is
// whether a hundred megabytes is worth moving to dodge one queued step.
func TestTheModelPricesAFetchTheWayTheEngineDoes(t *testing.T) {
	t.Parallel()

	steps := []Step{
		{Produces: layerOf(1), Size: 100 << 20},
		{Base: []ir.NodeID{layerOf(1)}, Produces: layerOf(2), Size: 1},
	}

	// A megabyte a second against steps of a second: the base is worth a
	// hundred steps and belongs where it already is.
	var r Rate

	r.Observe(1<<20, 1000, 1000)

	dear := PredictAt(steps, 2, 2, nil, &r)
	if dear.Transfers != 0 {
		t.Errorf("a hundred-megabyte base moved %d time(s) when priced,"+
			" want 0\n  it is worth a hundred steps and dodges one",
			dear.Transfers)
	}

	cheap := PredictWith(steps, 2, 2, nil)
	if cheap.Transfers != 1 {
		t.Errorf("an unpriced base moved %d time(s), want 1"+
			"\n  if it does not move here the test is not measuring price",
			cheap.Transfers)
	}
}
