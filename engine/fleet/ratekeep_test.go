package fleet_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// What the fleet costs survives the process that measured it.
//
// **Every real build is round one** (E350). A repeat inside one process delegates
// everything on its first pass, because an unmeasured fleet prices a transfer at
// nothing, and keeps two or three steps thereafter - 1.447s against 1.084s. Each
// `earth build` is a fresh process, so the faster behaviour is one the engine
// earns and then discards.
//
// The engine already keeps what a step read last time for exactly this reason
// (§4.6). What a fleet costs is the same kind of fact: measured, small, and
// useless to recompute (E351).
func TestWhatTheFleetCostsSurvivesTheProcess(t *testing.T) {
	t.Parallel()

	at := filepath.Join(t.TempDir(), "rate.json")

	var was fleet.Rate

	was.Observe(1<<20, 500, 1000)
	was.Observe(1<<10, 300, 1000)

	want := was.Slots(4 << 20)

	if err := was.Save(at); err != nil {
		t.Fatalf("%v", err)
	}

	var now fleet.Rate

	if err := now.Load(at); err != nil {
		t.Fatalf("%v", err)
	}

	if got := now.Slots(4 << 20); got != want {
		t.Errorf("a restored rate prices four megabytes at %d, want %d"+
			"\n  the knowledge that makes a fleet 1.51x rather than 1.13x is"+
			" thrown away when the process exits (E351)", got, want)
	}

	if !now.Measured() {
		t.Error("a restored rate reports itself unmeasured, so every decision" +
			" is made as though nothing were known")
	}
}

// A rate nobody has written is not an error.
//
// The first build on a machine has nothing to load, which is the ordinary case
// and not a failure: it measures, saves, and the next build starts where this
// one finished.
func TestAMissingRateIsNotAnError(t *testing.T) {
	t.Parallel()

	var r fleet.Rate

	if err := r.Load(filepath.Join(t.TempDir(), "absent.json")); err != nil {
		t.Errorf("loading a rate that has never been written: %v", err)
	}

	if r.Measured() {
		t.Error("a rate loaded from nothing reports itself measured")
	}
}

// A rate that will not parse is not an error either.
//
// It is a cache of something measurable, so the answer to a damaged one is to
// measure again - refusing a build over it would make an optimisation
// load-bearing (I5, I11).
func TestADamagedRateIsIgnored(t *testing.T) {
	t.Parallel()

	at := filepath.Join(t.TempDir(), "rate.json")

	if err := os.WriteFile(at, []byte("this is not json"), 0o600); err != nil {
		t.Fatalf("%v", err)
	}

	var r fleet.Rate

	if err := r.Load(at); err != nil {
		t.Errorf("a damaged rate failed a build: %v", err)
	}

	if r.Measured() {
		t.Error("a damaged rate was believed")
	}
}

// A driver loads what an earlier build measured, and keeps what this one did.
//
// **The mechanism and its use are different things**, and this project has met
// that five times (E331). `Save` and `Load` being right says nothing about
// whether a driver ever calls them, and a build that measures a fleet and
// forgets is the whole of E350.
func TestADriverKeepsWhatItLearns(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := &fleet.Layers{Root: root}

	var d fleet.Delegating

	d.Remember(store)
	d.NoteSpend(fleet.Reply{
		FetchedBytes: 1 << 20, FetchMillis: 500, DurationMillis: 1000,
	}, time.Second)
	d.NoteSpend(fleet.Reply{
		FetchedBytes: 1 << 10, FetchMillis: 300, DurationMillis: 1000,
	}, time.Second)

	if err := d.Keep(); err != nil {
		t.Fatalf("%v", err)
	}

	// A second build, on the same machine, against the same store.
	var next fleet.Delegating

	next.Remember(store)

	if !next.MeasuredForTest() {
		t.Error("a second build began knowing nothing, so it delegates" +
			" everything and keeps nothing, as round one always did (E351)")
	}
}
