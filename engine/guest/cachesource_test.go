package guest

import "testing"

// A cache that makes no claim resolves where it always did.
//
// **The property that keeps this change free.** Scoping every cache would move
// every cache directory on every machine at once - a slow build for everybody,
// in exchange for separating things that were never going to be confused,
// because a cache nobody has offered is served to nobody.
func TestAnUnscopedCacheKeepsItsDirectory(t *testing.T) {
	t.Parallel()

	if got, want := cacheSource("/s/mounts", Mount{ID: "go-mod"}), "/s/mounts/go-mod"; got != want {
		t.Errorf("cacheSource = %q, want %q\n  every existing cache directory"+
			" moves and every machine rebuilds from cold", got, want)
	}
}

// A claimed cache goes beneath its id, never beside it.
func TestAScopedCacheGoesBeneathItsID(t *testing.T) {
	t.Parallel()

	got := cacheSource("/s/mounts", Mount{ID: "go-mod", Scope: "beef"})

	if want := "/s/mounts/go-mod/beef"; got != want {
		t.Errorf("cacheSource = %q, want %q", got, want)
	}
}

// The two never meet, which is the whole point of the gate.
//
// One target claims its cache portable and another says nothing; they name one
// id and Κ₁ makes them different *steps* while saying nothing about the
// *directory*. If these resolved together, a fetch for the first would fill the
// directory the second runs against - another machine's bytes in a cache whose
// author never offered it.
func TestAClaimedAndAnUnclaimedCacheNeverMeet(t *testing.T) {
	t.Parallel()

	plain := cacheSource("/s/mounts", Mount{ID: "k"})
	claimed := cacheSource("/s/mounts", Mount{ID: "k", Scope: "beef"})

	if plain == claimed {
		t.Fatalf("both resolve to %q", plain)
	}

	// And the claimed one is *inside* the unclaimed one's directory rather than
	// a sibling, so a collector that knows about ids still finds it.
	if len(claimed) <= len(plain) || claimed[:len(plain)] != plain {
		t.Errorf("%q is not beneath %q, so an id no longer names everything"+
			" filed under it", claimed, plain)
	}
}
