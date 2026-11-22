package cache_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Placements survive the store.
//
// **The one thing that translates a traced read into a checkout path.** A `RUN`
// reports reading `/node/src/lib.rs`; only the `COPY` that put `node/` at
// `/node` knows that is `node/src/lib.rs` on disk. Carried in memory only, that
// correspondence is present on the build that ran the copy and absent on every
// build after it - which is every build, copies being the most cacheable step
// there is.
//
// Measured on midnight-node: 37 steps, 15 of them `COPY`, five served from L1
// with no placements, and `--auto-skip` therefore refused to record a key on
// every run. The `RUN cargo build` above them observed 808 reads perfectly and
// none of them could be named.
func TestPlacementsSurviveTheStore(t *testing.T) {
	t.Parallel()

	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	key := core.Key{9}
	want := []core.Placement{
		{Layer: "ctx", From: "node", To: "/node"},
		{Layer: "ctx", From: "pallets", To: "/pallets"},
	}

	c.Put(key, core.Entry{
		Layer: ir.NodeID{1}, Writer: "w", Declared: true, Placements: want,
	})

	got, ok := c.Get(key)
	if !ok {
		t.Fatal("the entry did not come back at all")
	}

	if len(got.Placements) != len(want) {
		t.Fatalf("%d placements came back, want %d - a cached COPY cannot say"+
			"\n  where it put anything, so no traced read can be named",
			len(got.Placements), len(want))
	}

	for i, p := range want {
		if got.Placements[i] != p {
			t.Errorf("placement %d came back as %+v, want %+v", i, got.Placements[i], p)
		}
	}
}
