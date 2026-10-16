package fleet

import "testing"

// A step prefers a machine whose cache mount is already warm.
//
// **The locality this engine could not see.** Placement models where a *layer*
// is and nothing else, so it cannot tell a worker that has built with `go-build`
// before from one whose directory is empty - and for the builds that matter that
// is the larger of the two costs. A cold Go build cache means recompiling what
// the warm machine beside it already holds, which is work rather than transfer,
// and no amount of layer affinity avoids it (E-F3).
//
// Inferred rather than announced, exactly as holding a base is: a worker that ran
// a step with cache id `k` made the directory and now has it. Nothing crosses the
// wire for this, and nothing in it can change a result (I5) - a warm machine that
// turns out to be cold recompiles, which is what would have happened anyway.
func TestAStepPrefersAMachineWithAWarmCache(t *testing.T) {
	t.Parallel()

	order := []joined{
		{id: "fleet-0", at: "a@host:1"},
		{id: "fleet-1", at: "b@host:2"},
		{id: "fleet-2", at: "c@host:3"},
	}

	got := preferFetching(order, nil, []string{"c@host:3"}, nil, transferCost)

	if len(got) != len(order) {
		t.Fatalf("preferring dropped workers: %d of %d", len(got), len(order))
	}

	if got[0].id != "fleet-2" {
		t.Errorf("asked %q first for a step whose cache is warm on fleet-2"+
			"\n  the step recompiles what the machine beside it already holds",
			got[0].id)
	}
}

// Warmth is a preference and not an exclusion.
//
// The same argument holders make: a warm machine can be busy, gone, or refuse
// the step, and falling through to a cold one is slower than the alternative and
// very much better than a failed build (I11).
func TestAColdMachineIsStillAsked(t *testing.T) {
	t.Parallel()

	order := []joined{{id: "fleet-0", at: "a@host:1"}, {id: "fleet-1", at: "b@host:2"}}

	got := preferFetching(order, nil, []string{"b@host:2"}, nil, transferCost)

	if len(got) != 2 {
		t.Fatalf("a cold machine was dropped rather than ranked: %d of 2", len(got))
	}
}

// Holding the base and holding the cache are different discounts, and they add.
//
// A machine with both should beat a machine with either, or the model has
// collapsed two facts into one and a fleet cannot tell "would not have to fetch"
// from "would not have to recompile".
func TestTheTwoDiscountsCompose(t *testing.T) {
	t.Parallel()

	order := []joined{
		{id: "base-only", at: "a@host:1"},
		{id: "warm-only", at: "b@host:2"},
		{id: "both", at: "c@host:3"},
	}

	got := preferFetching(order,
		[]string{"a@host:1", "c@host:3"}, // holds the base
		[]string{"b@host:2", "c@host:3"}, // warm cache
		nil, transferCost)

	if got[0].id != "both" {
		t.Errorf("asked %q first, ahead of the machine that needs neither a"+
			" fetch nor a recompile", got[0].id)
	}
}

// A busy warm machine loses to an idle cold one, at the stated price.
//
// The same calibration transferCost makes: affinity that ignores load puts every
// step of a parallel build on one machine while the others watch, which is worse
// than no affinity at all.
func TestLoadStillOutweighsWarmth(t *testing.T) {
	t.Parallel()

	order := []joined{{id: "warm-busy", at: "a@host:1"}, {id: "cold-idle", at: "b@host:2"}}

	got := preferFetching(order, nil, []string{"a@host:1"},
		map[string]int{"warm-busy": 2}, transferCost)

	if got[0].id != "cold-idle" {
		t.Errorf("asked the busy warm machine first; a warm cache is worth" +
			" less than a free slot or a fleet serialises onto one machine")
	}
}

// Warmth is remembered per cache id, and a step asking for one is not told about
// the other.
//
// The id is the name two steps agree on, and two steps naming different ids
// share nothing. A table that answered for any cache would send a step to a
// machine warm for something else entirely - advice that costs a placement and
// buys nothing.
func TestWarmthIsPerCacheID(t *testing.T) {
	t.Parallel()

	var w warmth

	w.also([]Cache{{ID: "go-mod"}}, "a@host:1")
	w.also([]Cache{{ID: "npm"}}, "b@host:2")

	for _, c := range []struct {
		id   string
		want string
	}{{"go-mod", "a@host:1"}, {"npm", "b@host:2"}} {
		got := w.of(Assignment{Op: Op{Caches: []Cache{{ID: c.id}}}})

		if len(got) != 1 || got[0] != c.want {
			t.Errorf("warm for %q: %v, want [%s]", c.id, got, c.want)
		}
	}

	if got := w.of(Assignment{Op: Op{Caches: []Cache{{ID: "cargo"}}}}); len(got) != 0 {
		t.Errorf("a cache nobody has filled reports %v as warm", got)
	}
}

// A worker with no address is not recorded.
//
// An in-process fleet has no address at all, and one sharing a store has nothing
// to be warm *elsewhere* about. An empty string in the table is a preference for
// a machine that cannot be named, which sorts every unnamed worker to the front.
func TestAnAddresslessWorkerIsNotWarm(t *testing.T) {
	t.Parallel()

	var w warmth

	w.also([]Cache{{ID: "go-mod"}}, "")

	if got := w.of(Assignment{Op: Op{Caches: []Cache{{ID: "go-mod"}}}}); len(got) != 0 {
		t.Errorf("recorded an addressless worker as warm: %v", got)
	}
}

// Two machines warm for one cache are both named, in the order they became warm,
// and neither twice.
//
// Ordered by first warmth rather than by map iteration, for the reason
// `holders.of` gives: a fleet's advice should not vary run to run.
func TestEveryWarmMachineIsNamedOnce(t *testing.T) {
	t.Parallel()

	var w warmth

	w.also([]Cache{{ID: "go-mod"}}, "a@host:1")
	w.also([]Cache{{ID: "go-mod"}}, "b@host:2")
	w.also([]Cache{{ID: "go-mod"}}, "a@host:1")

	got := w.of(Assignment{Op: Op{Caches: []Cache{{ID: "go-mod"}}}})

	if len(got) != 2 || got[0] != "a@host:1" || got[1] != "b@host:2" {
		t.Errorf("warm machines are %v, want [a@host:1 b@host:2] once each", got)
	}
}
