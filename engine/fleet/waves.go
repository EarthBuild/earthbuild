package fleet

// cheaperHere reports whether this machine finishes a step sooner than the
// fleet would, counting what shipping its inputs is worth.
//
// **One comparison where there were three thresholds.** "Do not ship what costs
// more than it saves" (E318), "do not queue behind a full fleet" (E320) and "do
// not keep what this machine has no room for" (E321) are the same question asked
// from different sides. Each arrived after a measurement showed the previous one
// moving a cost rather than removing it, which is what a set of one-sided
// thresholds does.
//
// Everything is in steps, and the unit that matters is a **wave**: a machine
// with `room` slots and `n` steps already running finishes one more after
// `ceil((n+1)/room)` of them. With two slots a machine runs 1, 2, 3 or 4 waves
// and nothing between, and the split that wins respects that - which is exactly
// what no per-side threshold could see (E321).
//
// `ship` is what moving the inputs is worth in whole steps, zero when this
// machine holds them or nobody has measured. It is added to the fleet's side
// because that is the side that would pay it.
//
// **Ties go to the fleet.** A step and its transfer being worth the same as
// running it here is the case where keeping it buys nothing and costs this
// machine a slot it needs for the decisions it alone can make.
//
// Room of zero is "as many as arrive", which is what a driver with no stated
// capacity has and what every build did before E321: one wave, always.
func cheaperHere(here, room, flight, slots int64, ship int) bool {
	return cheaperHereFetching(here, room, flight, slots, ship, 0)
}

// cheaperHereFetching is cheaperHere when this machine would have to fetch the
// step's inputs first.
//
// **A cost, not a veto.** Keeping a step used to require already holding
// everything it reads, on the argument that otherwise both choices move the same
// bytes and keeping buys a busy driver and nothing else. That holds only while
// the fleet has room: a driver sat out every level-shaped build, including one
// where a single worker was plainly saturated by a level of four and this
// machine had every slot free (E346, E347).
//
// `bring` goes on **this** side, because this is the side that would pay it, and
// the rest of the comparison is unchanged: a transfer here that overlaps a queue
// there is a step finished sooner, and waves already weigh those two things.
func cheaperHereFetching(here, room, flight, slots int64, ship, bring int) bool {
	// **In half-steps on both sides.** `Slots` is doubled so that its floor can
	// mean "half a step, never nothing", and the caller used to halve it before
	// comparing - which turned that floor into zero by integer division, so a
	// transfer smaller than a step was priced at exactly free.
	//
	// Measured: a chain of eight steps shipped every one of them, because at
	// each decision both sides finished in one wave and the transfer that would
	// make that possible cost nothing. The fleet was 17% slower than one machine
	// on work it could not parallelise at all (E343).
	return 2*waves(here, room)+int64(bring) < 2*waves(flight, slots)+int64(ship)
}

// waves is how many rounds a machine of this size needs to finish one more step.
//
// Zero room is unbounded - one wave whatever is running - which is what a
// machine that has not stated a capacity means. The **fleet** side is never
// passed zero: a fleet with no slots is a fleet with nowhere to put the step,
// which is not a cost comparison but the no-worker path, and the caller has
// already dealt with it.
func waves(running, room int64) int64 {
	if room <= 0 || running < 0 {
		return 1
	}

	return (running + room) / room
}
