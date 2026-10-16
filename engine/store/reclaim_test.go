package store_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/store"
)

// Nothing is reclaimed while the store has the room it was asked to keep free.
//
// **The default is always to keep.** A store is worth having because the next
// build reads it, so a collector that runs when it need not is a cache thrown
// away for nothing.
func TestAStoreWithRoomReclaimsNothing(t *testing.T) {
	t.Parallel()

	got, want := store.CeilingFor(100, 20, 50), uint64(0)
	if got != want {
		t.Errorf("a store of 100 with 50 free and 20 wanted collects to %d", got)
	}
}

// Short of room, the store gives up exactly the shortfall.
//
// **Exactly, rather than down to some fraction**, because every byte past the
// shortfall is a layer somebody will rebuild. A store of 100 with 5 free that
// wants 20 is 15 short, so it collects to 85.
func TestAShortStoreGivesUpTheShortfall(t *testing.T) {
	t.Parallel()

	if got := store.CeilingFor(100, 20, 5); got != 85 {
		t.Errorf("a store of 100 with 5 free and 20 wanted collects to %d, wanted 85", got)
	}
}

// A shortfall larger than the store collects the whole store rather than
// underflowing to something enormous - which, as an unsigned ceiling, would read
// as "keep everything" and reclaim nothing at all.
func TestAShortfallLargerThanTheStoreDoesNotUnderflow(t *testing.T) {
	t.Parallel()

	if got := store.CeilingFor(10, 500, 0); got != 0 {
		t.Errorf("a store of 10 needing 500 collects to %d, wanted 0", got)
	}
}
