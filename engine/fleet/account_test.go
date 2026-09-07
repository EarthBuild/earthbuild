package fleet_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A delegated step accounts for its transfer and its compute separately.
//
// The whole point of the accounting. A fleet that is no faster than one machine
// is a common outcome (rebuck PR 10) and an uninformative one: the question is
// whether the time went into moving inputs, into waiting for a worker, or into
// the step itself, because each has a different remedy and only one of them is
// "the work was not parallel enough".
func TestADelegatedStepAccountsForTransferAndComputeApart(t *testing.T) {
	t.Parallel()

	f := &fleet.InProcess{}

	f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
		return fleet.Reply{
			Version:        fleet.Version,
			Layer:          ir.NodeID{2},
			FetchedBytes:   4 << 20,
			FetchMillis:    700,
			DurationMillis: 300,
		}, nil
	})

	d := &fleet.Delegating{Local: &countingLocal{}, Fleet: f}

	_, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"}, nil, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	s := d.Spend()

	if s.Delegated != 1 || s.Local != 0 {
		t.Errorf("counted %d delegated and %d local, want 1 and 0",
			s.Delegated, s.Local)
	}

	if s.Fetched != 4<<20 {
		t.Errorf("accounted %d transferred bytes, want %d", s.Fetched, 4<<20)
	}

	if s.Fetching != 700*time.Millisecond {
		t.Errorf("accounted %v fetching, want 700ms", s.Fetching)
	}

	if s.Computing != 300*time.Millisecond {
		t.Errorf("accounted %v computing, want 300ms", s.Computing)
	}
}

// The driver measures the round trip itself, and the difference is the overhead.
//
// A worker's own numbers cover what it did; they cannot cover what it was doing
// nothing for - a control message queued behind another, a connection being
// opened, a scheduler that had not placed the step yet. That gap is exactly the
// symptom of "embarrassingly parallel and yet no faster", so it is measured on
// this side rather than asked for.
func TestTheDriverMeasuresWhatTheWorkerCannotSee(t *testing.T) {
	t.Parallel()

	f := &fleet.InProcess{}

	f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
		time.Sleep(40 * time.Millisecond)

		// Claims to have spent no time at all, so every measured millisecond is
		// unaccounted for by the worker.
		return fleet.Reply{Version: fleet.Version, Layer: ir.NodeID{2}}, nil
	})

	d := &fleet.Delegating{Local: &countingLocal{}, Fleet: f}

	_, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"}, nil, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	s := d.Spend()
	if s.Overhead < 20*time.Millisecond {
		t.Errorf("accounted %v of overhead for a worker that slept 40ms and"+
			" claimed nothing\n  the gap between the round trip and what the"+
			" worker admits to is the part nobody else can see", s.Overhead)
	}
}

// Timings from a worker are advisory and cannot reach the result.
//
// A5: the reply is a claim. If an accounting field could change what a step is
// keyed on, a worker could alter another machine's build by lying about how long
// it took - which is why these are counted and then dropped, never carried into
// the result.
func TestTimingsFromAWorkerDoNotReachTheResult(t *testing.T) {
	t.Parallel()

	honest := fleet.Reply{Version: fleet.Version, Layer: ir.NodeID{2}, Content: ir.NodeID{3}}

	liar := honest
	liar.FetchMillis = 1 << 40
	liar.DurationMillis = -99
	liar.FetchedBytes = -1

	for _, tc := range []struct {
		name  string
		reply fleet.Reply
	}{{"honest", honest}, {"absurd", liar}} {
		f := &fleet.InProcess{}
		f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
			return tc.reply, nil
		})

		d := &fleet.Delegating{Local: &countingLocal{}, Fleet: f}

		got, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"}, nil, nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		if got.Layer != (ir.NodeID{2}) || got.Content != (ir.NodeID{3}) {
			t.Errorf("%s: the result changed with the timings: %+v", tc.name, got)
		}
	}
}

// The report names where the time went, not merely how much there was.
//
// A count without a cause is the failure this whole account exists to avoid: a
// number that says a fleet build took eleven minutes tells nobody what to do
// next. Transfer-bound, overhead-bound and compute-bound have three different
// remedies, and the report has to say which one it saw.
func TestTheReportNamesTheBottleneck(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		spend fleet.Spend
		want  string
	}{
		{
			name: "transfer bound",
			spend: fleet.Spend{
				Delegated: 10, Fetched: 900 << 20,
				Fetching: 90 * time.Second, Computing: 10 * time.Second,
			},
			want: "transfer",
		},
		{
			name: "overhead bound",
			spend: fleet.Spend{
				Delegated: 10,
				Computing: 5 * time.Second, Overhead: 80 * time.Second,
			},
			want: "overhead",
		},
		{
			name: "compute bound",
			spend: fleet.Spend{
				Delegated: 10,
				Fetching:  2 * time.Second, Computing: 90 * time.Second,
			},
			want: "compute",
		},
	} {
		got := tc.spend.Report()
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s reported %q, which does not name %q"+
				"\n  a total without a cause is a number nobody can act on",
				tc.name, got, tc.want)
		}
	}
}

// A build that delegated nothing does not report a fleet's timings.
//
// Zero of everything divided by zero steps is not "compute bound", it is no
// evidence at all, and a report that named a bottleneck from it would be
// inventing one.
func TestAnEmptyAccountClaimsNoBottleneck(t *testing.T) {
	t.Parallel()

	got := fleet.Spend{Local: 7}.Report()
	for _, w := range []string{"transfer", "overhead", "compute"} {
		if strings.Contains(got, w) {
			t.Errorf("an account with no delegated steps reported %q", got)
		}
	}

	// And says which of the two silences it is. "No time was accounted for"
	// describes a fleet that ran steps and measured nothing, which is a bug;
	// "nothing was delegated" describes a build that never used the fleet, which
	// is a configuration. Reading one as the other sends somebody to the wrong
	// half of the system.
	if !strings.Contains(got, "nothing was delegated") {
		t.Errorf("reported %q for a build that delegated nothing", got)
	}

	if !strings.Contains(got, "7") {
		t.Errorf("reported %q, which does not say how many steps ran here", got)
	}
}

// The account separates waiting for a slot from waiting for a network.
//
// **They were one number, and they mean opposite things.** A queue says the
// fleet is being used and more machines would help; wire time says the fleet is
// expensive and more machines would make it worse. Reported together, the
// account could not answer the only question anybody asks it.
//
// Measured at four workers the combined figure was a fixed 500ms a step and its
// composition was unknown (E335, E336).
func TestTheAccountSeparatesQueueingFromTheWire(t *testing.T) {
	t.Parallel()

	var d fleet.Delegating

	d.NoteSpend(fleet.Reply{
		DurationMillis: 100,
		QueueMillis:    250,
		FetchMillis:    50,
	}, 500*time.Millisecond)

	got := d.Spend()

	if got.Queueing != 250*time.Millisecond {
		t.Errorf("queueing counted as %v, want 250ms", got.Queueing)
	}

	// 500 of round trip, less 100 of step, 250 of queue and 50 of transfer.
	if got.Overhead != 100*time.Millisecond {
		t.Errorf("the wire counted as %v, want 100ms - the rest is the"+
			" worker's own account of itself (E336)", got.Overhead)
	}
}

// The account says how many transfers there were and how slow the worst was.
//
// **A total is not a distribution.** "transfer 2.9s" across four workers is one
// slow fetch or twenty ordinary ones, and those have nothing in common: the
// first is a peer or a layer to look at, the second is a per-fetch cost to
// remove. Three experiments have now been spent narrowing a total by
// subtraction (E335, E336, E337) when a count would have said which.
func TestTheAccountCountsTransfersAndNamesTheWorst(t *testing.T) {
	t.Parallel()

	var d fleet.Delegating

	d.NoteSpend(fleet.Reply{FetchMillis: 100, FetchedBytes: 10}, time.Second)
	d.NoteSpend(fleet.Reply{FetchMillis: 700, FetchedBytes: 10}, time.Second)
	d.NoteSpend(fleet.Reply{DurationMillis: 5}, time.Second) // fetched nothing

	got := d.Spend()

	if got.Fetches != 2 {
		t.Errorf("%d transfers counted, want 2 - a step that fetched nothing"+
			" did not transfer", got.Fetches)
	}

	if got.Slowest != 700*time.Millisecond {
		t.Errorf("the slowest transfer was %v, want 700ms", got.Slowest)
	}
}

// What one round cost, against what every round has cost.
//
// **The fleet varies by 47% and a single machine by 0.6%** (E349), so the
// question is what differs between rounds - and the account is cumulative, so it
// cannot say. A difference of two totals is what one round did.
//
// Subtraction rather than a per-round account: there is one fleet and one set of
// counters, and a second set kept in step with the first would be a second thing
// to get wrong (E350).
func TestOneRoundIsTheDifferenceOfTwoTotals(t *testing.T) {
	t.Parallel()

	var d fleet.Delegating

	d.NoteSpend(fleet.Reply{DurationMillis: 10, FetchMillis: 5, FetchedBytes: 100},
		20*time.Millisecond)

	was := d.Spend()

	d.NoteSpend(fleet.Reply{DurationMillis: 30, FetchMillis: 7, FetchedBytes: 900},
		50*time.Millisecond)
	d.NoteSpend(fleet.Reply{DurationMillis: 30}, 50*time.Millisecond)

	got := d.Spend().Since(was)

	if got.Delegated != 2 {
		t.Errorf("a round of two delegated steps counted %d", got.Delegated)
	}

	if got.Fetched != 900 {
		t.Errorf("a round that moved 900 bytes counted %d", got.Fetched)
	}

	if got.Fetches != 1 {
		t.Errorf("a round with one transfer counted %d", got.Fetches)
	}

	if got.Computing != 60*time.Millisecond {
		t.Errorf("a round of two 30ms steps computed for %v", got.Computing)
	}

	// The slowest is not a difference: it is the worst of the whole build, and
	// subtracting it would say a round's worst transfer was negative whenever
	// the record stood from an earlier one.
	if got.Slowest != 7*time.Millisecond {
		t.Errorf("the slowest transfer of this round was %v, want 7ms",
			got.Slowest)
	}
}
