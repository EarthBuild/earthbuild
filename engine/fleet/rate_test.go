package fleet

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// With nothing measured, fetching costs what it always did.
//
// The fallback has to be exactly today's behaviour, or every build that has not
// yet moved a byte gets a placement decision made from a number nobody has.
func TestAnUnmeasuredFleetCostsAFetchAsBefore(t *testing.T) {
	t.Parallel()

	var r Rate

	if got := r.Slots(1 << 30); got != transferCost {
		t.Errorf("an unmeasured fleet prices a 1 GiB fetch at %d, want %d"+
			"\n  a guess is fine and a guess dressed as a measurement is not",
			got, transferCost)
	}
}

// A fetch is priced against what a step is worth, both measured.
//
// **The number `transferCost` was standing in for.** Its own comment called it
// "a model rather than a measurement ... one line to change when there is a
// measurement to change it to" - and E315 took the measurement: 1.6 MiB in
// 245ms against steps of 400ms.
//
// The point is that it *scales*. Half a step-slot is about right for a 1.6 MiB
// base and absurd for a 500 MB one, which at the same rate takes two minutes and
// is worth three hundred steps. A constant makes a fleet delegate work whose
// inputs cost more to ship than the work is worth, which is the failure the
// second attempt at this project reported and never explained.
func TestAFetchIsPricedAgainstWhatAStepIsWorth(t *testing.T) {
	t.Parallel()

	var r Rate

	// A megabyte a second, and steps worth a second each.
	r.Observe(1<<20, 1000, 1000)

	// A base of a megabyte is one step's work: two half-slots, doubled.
	if got := r.Slots(1 << 20); got != 2 {
		t.Errorf("a fetch worth one step priced at %d, want 2 (doubled)", got)
	}

	// Ten megabytes is ten steps.
	if got := r.Slots(10 << 20); got != 20 {
		t.Errorf("a fetch worth ten steps priced at %d, want 20 (doubled)", got)
	}
}

// A fetch nobody can size is priced as one nobody measured.
//
// A zero here means "not known", not "free". Pricing an unknown base at nothing
// would make the cheapest possible worker the one that has to fetch the most,
// which is not a degraded answer but an inverted one.
func TestAFetchOfUnknownSizeIsNotFree(t *testing.T) {
	t.Parallel()

	var r Rate

	r.Observe(1<<20, 1000, 1000)

	if got := r.Slots(0); got != transferCost {
		t.Errorf("a base of unstated size priced at %d, want %d", got, transferCost)
	}
}

// A worker with an unshipped base loses to a busy holder when the base is big.
//
// The whole point of the exercise, at the level it decides something: with a
// fixed cost a holder that is one step busier always loses, whatever it is
// holding. Once the base is worth ten steps, it does not.
func TestABigBaseKeepsAStepWhereTheBytesAre(t *testing.T) {
	t.Parallel()

	order := []joined{
		{id: "a", at: "a@1", capacity: 4},
		{id: "b", at: "b@2", capacity: 4},
	}

	busy := map[string]int{"a": 2}

	// Small base: the idle machine wins, as it does today.
	got := preferFetching(order, []string{"a@1"}, busy, transferCost)
	if got[0].id != "b" {
		t.Errorf("a cheap base went to %q, want the idle machine", got[0].id)
	}

	// A base worth ten steps: worth waiting for the holder.
	got = preferFetching(order, []string{"a@1"}, busy, 20)
	if got[0].id != "a" {
		t.Errorf("a base worth ten steps went to %q, want the machine that"+
			" already has it", got[0].id)
	}
}

// A rendezvous prices a fetch from what its own fleet has done.
//
// The wiring, and the reason `Rate` is not merely a calculator: the two numbers
// it needs - what a transfer cost and what a step was worth - arrive on every
// reply already (`FetchedBytes`, `FetchMillis`, `DurationMillis`). Nothing new
// has to be measured, only stopped being thrown away.
func TestARendezvousPricesAFetchFromItsOwnFleet(t *testing.T) {
	t.Parallel()

	var r Rendezvous

	// Steps worth a second, and a network doing a megabyte a second.
	r.observed(Reply{FetchedBytes: 1 << 20, FetchMillis: 1000, DurationMillis: 1000})

	if got := r.priceOf(Assignment{Hints: Hints{Bytes: 10 << 20}}); got != 20 {
		t.Errorf("a ten-step base priced at %d slot(s), want 20"+
			"\n  the numbers are already on every reply (E317)", got)
	}

	if got := r.priceOf(Assignment{}); got != transferCost {
		t.Errorf("a base of unstated size priced at %d, want %d",
			got, transferCost)
	}
}

// A driver states how big a step's inputs are, when it knows.
//
// The size has to come from the driver: the worker learns it by fetching, which
// is exactly the decision the number exists to inform. `Delegating` sees every
// layer its own steps produced and can be told about the ones it did not.
//
// **All of them or none.** A partial sum reads as a full price, and a base
// priced at a tenth of its size is worse than one priced at the constant -
// under-pricing is how a fleet talks itself into shipping something it should
// not have.
func TestADriverStatesWhatItKnowsAboutSize(t *testing.T) {
	t.Parallel()

	known, unknown := ir.NodeID{1}, ir.NodeID{2}

	d := &Delegating{Sizes: func(id ir.NodeID) int64 {
		if id == known {
			return 4096
		}

		return 0
	}}

	if got := d.bytesOf(Assignment{Base: []ir.NodeID{known}}); got != 4096 {
		t.Errorf("a step on a 4096-byte base is stated as %d bytes", got)
	}

	if got := d.bytesOf(Assignment{Base: []ir.NodeID{known, unknown}}); got != 0 {
		t.Errorf("a step with one input of unknown size is stated as %d bytes,"+
			" want 0\n  a partial sum reads as a full price (E317)", got)
	}
}

// What a step is predicted to read is priced as a fragment, not as a base.
//
// **The driver was pricing a lazy transfer as a whole one.** `Hints.Bytes` is
// the size of the inputs, which is what crosses when a worker fetches whole
// layers - and with a prediction it fetches about a hundredth of that (E323).
// Measured at four workers: a 16 MB base moved 1.1 MiB in total, and the driver
// went on deciding as though each step would move 16 (E326).
//
// It cannot know a fragment's size in advance, and it does not have to: every
// reply says what that step actually fetched. The typical figure is what a
// prediction is worth, and the stated size is what it falls back to.
func TestAPredictedReadIsPricedAsAFragment(t *testing.T) {
	t.Parallel()

	var r Rate

	// A megabyte a second, steps of a second, and steps that actually fetched
	// about a hundredth of a 100 MB base.
	r.Observe(1<<20, 1000, 1000)
	r.Observe(1<<20, 1000, 1000)

	big := int64(100 << 20)

	if got := r.Slots(big); got < 100 {
		t.Fatalf("a hundred-megabyte base priced at %d, want a lot", got)
	}

	if got := r.Typical(); got != 1<<20 {
		t.Errorf("a step typically fetched %d bytes, want %d", got, 1<<20)
	}

	// Priced by what steps actually move, a fragment is two slots, not two
	// hundred.
	if got := r.Slots(r.Typical()); got != 2 {
		t.Errorf("a fragment priced at %d slot(s), want 2", got)
	}
}

// A fleet that has fetched nothing has no typical fetch.
//
// The fallback has to be "no answer" rather than zero: a zero would price every
// predicted step as free, and free is the answer that sends work to a machine
// that has the most to move (E317).
func TestAFleetThatFetchedNothingHasNoTypicalFetch(t *testing.T) {
	t.Parallel()

	var r Rate

	r.Observe(0, 0, 1000) // a warm step: it ran, it fetched nothing

	if got := r.Typical(); got != 0 {
		t.Errorf("a fleet that has fetched nothing reports a typical fetch of"+
			" %d", got)
	}
}

// A transfer costs something before it has moved a byte.
//
// **A fetch of a four-kilobyte layer costs hundreds of milliseconds** between
// machines, almost none of it the bytes - E337 measured the transport
// contributing nothing at all for 26ms of a local fetch. `Slots` computed
// bytes x time / bytes, purely proportional, so a small layer was free and
// spreading a level of work over every machine looked costless.
//
// That is wrong as arithmetic whatever it does to a wall clock: a transfer is a
// request, a round trip and an answer before it is any bytes. What it is *not*
// is a measured cause of anything yet - see E346, where the run that suggested
// it did not reproduce.
//
// The fixed cost is measured, not assumed: the least any observed fetch has
// taken is a bound on what the next one cannot beat.
func TestATransferCostsSomethingBeforeItMovesAByte(t *testing.T) {
	t.Parallel()

	var r Rate

	// A fetch of a megabyte took 500ms; a fetch of a kilobyte took 300ms. Most
	// of both is not the bytes.
	r.Observe(1<<20, 500, 1000)
	r.Observe(1<<10, 300, 1000)

	// A kilobyte is not free: it costs about what the cheapest fetch cost.
	small := r.Slots(1 << 10)
	if small < 1 {
		t.Errorf("a kilobyte priced at %d half-step(s); the cheapest fetch"+
			" anybody has seen took 300ms of a 1000ms step (E346)", small)
	}

	// And a large one still costs more than a small one.
	if big := r.Slots(64 << 20); big <= small {
		t.Errorf("64 MiB priced at %d and a kilobyte at %d", big, small)
	}
}

// The price converges on what the fleet usually costs.
//
// **Written to prove a decaying mean was needed, and it proved the opposite.**
// E351 saw a second build keep ten steps of sixteen on evidence gathered when
// the fleet was cold, and the obvious remedy was an estimator that forgets. This
// test was the red for it, and it was green: fifty ordinary fetches already
// bury one slow one, because a cumulative mean over N samples gives an outlier
// weight 1/N.
//
// So the mechanism was not written (E352). What is kept is the property that
// made it unnecessary, which nothing else asserts: an implementation that
// believed its most recent observation, or its first, would fail here.
func TestThePriceConvergesOnWhatTheFleetUsuallyCosts(t *testing.T) {
	t.Parallel()

	var r Rate

	// One very slow fetch, as a cold build sees.
	r.Observe(1<<20, 4000, 1000)

	cold := r.Slots(1 << 20)

	// Then a great many ordinary ones.
	for range 50 {
		r.Observe(1<<20, 100, 1000)
	}

	warm := r.Slots(1 << 20)

	t.Logf("a megabyte priced at %d half-step(s) cold and %d warm", cold, warm)

	if warm >= cold {
		t.Errorf("after fifty ordinary fetches a megabyte still prices at %d,"+
			" against %d when the only evidence was one cold one", warm, cold)
	}

	// And it has actually converged on the ordinary cost - a hundred
	// milliseconds against a thousand-millisecond step is one half-step, not
	// four.
	if warm > 2 {
		t.Errorf("a megabyte that takes 100ms of a 1000ms step prices at %d"+
			" half-step(s); the cold sample is still dominating", warm)
	}
}
