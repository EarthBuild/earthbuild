package fleet

import "testing"

// Where a step goes is one comparison: which side finishes it sooner.
//
// **Three rules collapse into this.** "Do not ship what costs more than it
// saves" (E318), "do not queue behind a full fleet" (E320) and "do not keep what
// this machine has no room for" (E321) are all the same question asked from
// different sides, and each answered it with its own threshold. Measured, the
// split that wins respects **wave granularity** - with two slots a machine runs
// 1, 2, 3 or 4 waves and nothing between - which no per-side threshold can see.
//
// Costs are in **half-steps**, which is the resolution the price of a transfer
// has: `Slots` floors at one, meaning "half a step, never nothing". A machine
// with `room` slots and `n` already running finishes one more after
// `ceil((n+1)/room)` waves; the fleet pays that plus what shipping the inputs is
// worth. Ties go to the fleet, which keeps machines busy - but a transfer is
// never a tie, because it is never free (E343).
func TestWhereAStepGoesIsOneComparison(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name                      string
		here, room, flight, slots int64
		ship                      int
		want                      bool
	}{
		{
			name: "both idle, nothing to ship: the fleet, because a tie does",
			room: 2, slots: 2, want: false,
		},
		{
			name: "both idle and a transfer worth half a step: here",
			room: 2, slots: 2, ship: 1, want: true,
		},
		{
			name: "both idle, a base worth ten steps: here",
			room: 2, slots: 2, ship: 20, want: true,
		},
		{
			name: "fleet full, this machine free: here",
			room: 2, slots: 1, flight: 1, want: true,
		},
		{
			name: "both full: the fleet, which starts the moment it can",
			here: 1, room: 1, flight: 1, slots: 1, want: false,
		},
		{
			name: "this machine a wave behind: the fleet",
			here: 2, room: 2, slots: 2, want: false,
		},
		{
			name: "the fleet a wave behind: here",
			room: 2, flight: 2, slots: 2, want: true,
		},
		{
			name: "the fleet a wave behind but the base is dear: still here",
			room: 2, flight: 2, slots: 2, ship: 10, want: true,
		},
		{
			name: "this machine two waves behind, a dear base: here anyway",
			here: 4, room: 2, slots: 2, ship: 20, want: true,
		},
		{
			name: "no stated room means as many as arrive",
			here: 9, flight: 1, slots: 1, want: true,
		},
	} {
		if got := cheaperHere(c.here, c.room, c.flight, c.slots, c.ship); got != c.want {
			t.Errorf("%s: kept here = %v, want %v", c.name, got, c.want)
		}
	}
}

// A worker that has not spoken yet is assumed to be a machine like this one.
//
// **The cold start, again, and it cost a whole run.** Capacity arrives on a
// reply, so eight steps deciding at once all size the fleet at one slot per
// worker - and one measured run kept seven of eight steps and finished no faster
// than a single machine, while two others split four and four and finished in
// two thirds of the time (E322).
//
// Assuming a worker is at least as roomy as the machine doing the asking is not
// arbitrary: it is the only other machine this process has ever seen, and a
// fleet is normally made of peers. It is corrected by the first reply, upwards
// or downwards, and `roomy` keeps the largest anybody has admitted to.
func TestAnUnheardWorkerIsSizedLikeThisMachine(t *testing.T) {
	t.Parallel()

	d := &Delegating{Room: 4, Fleet: &countingTransport{}}

	if got := d.slots(); got != 4 {
		t.Errorf("a fleet of one unheard worker was sized at %d slot(s), want 4"+
			"\n  sizing it at one keeps work that should have gone (E322)", got)
	}

	d.roomy(6)

	if got := d.slots(); got != 6 {
		t.Errorf("a worker that admitted to six was sized at %d", got)
	}
}

// What this machine would have to fetch is a cost, not a veto.
//
// **The driver sat out every level-shaped build** (E346). From the second level
// on, every step stands on a layer a worker made, so `holdsAll` was false and
// keeping was never on offer - even with a single worker plainly saturated by a
// level of four, and four idle slots on the machine doing the asking.
//
// The gate's argument was that both choices move the bytes, so keeping buys a
// busy driver and nothing else. That holds only while the fleet has room. When
// it does not, a transfer here that overlaps with a queue there is a step
// finished sooner, and the comparison already knows how to weigh those two
// things (E347).
func TestWhatThisMachineWouldFetchIsACostNotAVeto(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name                      string
		here, room, flight, slots int64
		ship, bring               int
		want                      bool
	}{
		{
			name: "the fleet has room and holds it: send it there",
			room: 2, slots: 4, bring: 4, want: false,
		},
		{
			name: "the fleet is two waves behind and this machine is idle: fetch it",
			room: 2, flight: 8, slots: 4, bring: 1, want: true,
		},
		{
			name: "the fleet is behind but fetching costs more than waiting",
			room: 2, flight: 4, slots: 4, bring: 8, want: false,
		},
		{
			name: "nothing to fetch and nothing to ship: the fleet, as a tie does",
			room: 2, slots: 2, want: false,
		},
	} {
		got := cheaperHereFetching(c.here, c.room, c.flight, c.slots, c.ship, c.bring)
		if got != c.want {
			t.Errorf("%s: kept here = %v, want %v", c.name, got, c.want)
		}
	}
}
