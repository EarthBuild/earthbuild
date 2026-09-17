package exec

import (
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Only a cache the author offered, and only one something can read, is shared.
//
// **Two flags and both are required.** `--portable-except` says the contents may
// cross; `--helper` says what crossing means. A mount with the claim and no
// helper is a cache nobody can take apart into units, and a mount with a helper
// and no claim is one whose author never offered it - each is a directory this
// machine keeps to itself, which is what every cache mount was before any of
// this.
func TestOnlyAnOfferedAndReadableCacheIsShared(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name  string
		mount ir.Mount
		want  bool
	}{
		{"neither", ir.Mount{ID: "k", Target: "/c"}, false},
		{"claim only", ir.Mount{ID: "k", Target: "/c", Portable: true}, false},
		{"helper only", ir.Mount{ID: "k", Target: "/c", Helper: "./h.wasm"}, false},
		{"both", ir.Mount{ID: "k", Target: "/c", Portable: true, Helper: "./h.wasm"}, true},
		// An ephemeral cache is made for the step and removed with it, so there
		// is nothing for another machine to be given.
		{"ephemeral", ir.Mount{Target: "/c", Ephemeral: true, Portable: true, Helper: "./h.wasm"}, false},
		// A persisted cache is captured into the layer, so its contents *are*
		// the result and travel as one.
		{"persisted", ir.Mount{ID: "k", Target: "/c", Persist: true, Helper: "./h.wasm"}, false},
	} {
		got := shareable([]ir.Mount{c.mount}, "/s/mounts", "")

		if (len(got) == 1) != c.want {
			t.Errorf("%s: shared=%v, want %v", c.name, len(got) == 1, c.want)
		}
	}
}

// A shared cache is found where the guest put it.
//
// The directory is `<mounts>/<id>/<scope>`, which is `cacheSource` said from the
// other side of the boundary. Two places computing one path is a drift waiting
// to happen, and the scope half of it is why: an unclaimed cache has no scope
// and a claimed one does, so a reader using the wrong rule finds an empty
// directory and reports an empty cache.
func TestASharedCacheIsFoundWhereTheGuestPutIt(t *testing.T) {
	t.Parallel()

	m := ir.Mount{ID: "go-mod", Target: "/c", Portable: true, Helper: "./h.wasm"}

	got := shareable([]ir.Mount{m}, "/s/mounts", "")
	if len(got) != 1 {
		t.Fatalf("a portable cache with a helper was not shared")
	}

	if want := filepath.Join("/s/mounts", "go-mod", m.Scope("")); got[0].dir != want {
		t.Errorf("looked in %q, want %q", got[0].dir, want)
	}
}

// A trust domain reaches the directory too.
//
// The same domain that scoped the mount has to scope the lookup, or the export
// reads one directory while the step wrote another.
func TestTheDomainReachesTheLookup(t *testing.T) {
	t.Parallel()

	m := ir.Mount{ID: "k", Target: "/c", Portable: true, Helper: "./h.wasm"}

	plain := shareable([]ir.Mount{m}, "/s/mounts", "")
	fork := shareable([]ir.Mount{m}, "/s/mounts", "fork")

	if len(plain) != 1 || len(fork) != 1 {
		t.Fatal("a portable cache was not shared")
	}

	if plain[0].dir == fork[0].dir {
		t.Error("two trust domains share one directory, so a fork's units are" +
			" exported as though a protected branch had made them")
	}
}

// Nothing to share is not an error and not a walk.
func TestNothingToShareIsNothing(t *testing.T) {
	t.Parallel()

	if got := shareable(nil, "/s/mounts", ""); len(got) != 0 {
		t.Errorf("a step with no mounts offered %d caches", len(got))
	}
}
