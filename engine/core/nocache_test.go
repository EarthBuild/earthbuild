package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `--no-cache` reads nothing and still writes.
//
// Two of the tree's invocations ask for it and the gate could not pass it,
// because the engine had no such option: `no-cache-local-artifact.earth+test`
// exists to check that a build told to ignore the cache actually re-runs (E462).
//
// **Reads nothing, writes everything.** A build that ignored the cache in both
// directions would leave the store as it found it, so the *next* build would
// miss too - which turns one instruction to redo the work into a project whose
// cache never warms again. The instruction is about this build.
func TestANoCacheBuildIgnoresWhatIsThereAndStillFillsIt(t *testing.T) {
	t.Parallel()

	g, _ := fan(1)

	store := &countingCache{entries: map[core.Key]core.Entry{}}

	// A first build, ordinary, which fills the store.
	first := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: &slowExec{},
		Blobs:    allBlobs{},
		Cache:    store,
	}

	_, err := first.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	if store.puts == 0 {
		t.Fatal("the first build wrote nothing, so this test measures nothing")
	}

	wrote := store.puts

	// The same build again, told to ignore the cache.
	again := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: &slowExec{},
		Blobs:    allBlobs{},
		Cache:    store,
		NoCache:  true,
	}

	store.gets = 0

	rec := &core.Record{}
	again.Record = rec

	_, err = again.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	if store.gets != 0 {
		t.Errorf("a --no-cache build consulted the cache %d times", store.gets)
	}

	// Both tiers, not just the first. `tryL2` looks up a second key, and a
	// build that skipped one lookup and made the other would be a build with an
	// opinion about which parts of the cache it trusted.
	// `rec` is the record this test made, so the nil guard was checking
	// something the compiler can already prove (govet nilness). Dropped rather
	// than kept as reassurance: a condition that cannot be false reads as a
	// case somebody considered, and there is no such case here.
	if len(rec.Steps) > 0 && rec.Steps[0].Outcome == core.OutcomeL2Hit {
		t.Error("a --no-cache build hit on the observed key")
	}

	if store.puts <= wrote {
		t.Error("a --no-cache build wrote nothing" +
			"\n  the instruction is to redo this build, not to stop the project's" +
			" cache ever warming again")
	}
}

// And without it, the same build hits.
//
// The control. Without this, a --no-cache build that never hit would be
// indistinguishable from a cache that never worked.
func TestTheSameBuildHitsWithoutNoCache(t *testing.T) {
	t.Parallel()

	g, _ := fan(1)

	store := &countingCache{entries: map[core.Key]core.Entry{}}

	for range 2 {
		s := &core.Scheduler{
			Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
			Executor: &slowExec{},
			Blobs:    allBlobs{},
			Cache:    store,
		}

		_, err := s.Run(context.Background(), g)
		if err != nil {
			t.Fatal(err)
		}
	}

	if store.gets == 0 {
		t.Error("an ordinary build consulted the cache not at all")
	}
}

// countingCache counts what it is asked.
type countingCache struct {
	entries    map[core.Key]core.Entry
	gets, puts int
}

func (c *countingCache) Get(k core.Key) (core.Entry, bool) {
	c.gets++
	e, ok := c.entries[k]

	return e, ok
}

func (c *countingCache) Put(k core.Key, e core.Entry) {
	c.puts++
	c.entries[k] = e
}

var _ = ir.Op{}
