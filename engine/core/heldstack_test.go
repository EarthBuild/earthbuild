package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// someBlobs holds exactly the ids it was given.
type someBlobs map[ir.NodeID]bool

func (b someBlobs) Has(id ir.NodeID) bool { return b[id] }

// oneEntry is an action cache holding a single entry under a single key.
type oneEntry struct {
	key core.Key
	e   core.Entry
}

func (c oneEntry) Get(k core.Key) (core.Entry, bool) {
	if k != c.key {
		return core.Entry{}, false
	}

	return c.e, true
}

func (c oneEntry) Put(core.Key, core.Entry) {}

// A stack is all-or-nothing, every layer of it.
//
// **An image is many layers, and an entry names all of them.** `held` checks
// each, and its own comment says why: "a hit that materialised some of an
// image's layers would produce a filesystem missing an element, and the build
// above it could not tell that from a complete one. Checking the first layer
// and trusting the rest is the same mistake as checking none."
//
// The rule was right and unguarded. Deleting the loop over `Layers` - so only
// `Layer` is checked - passed the whole suite: `SURVIVED core: held checking
// the first layer and trusting the stack`, found by adding Λ to a mutation
// catalogue of 486 entries that did not reach it.
//
// What it would cost is not a slow build. A `FROM` whose third layer has been
// collected is served as a hit, the stack materialises without it, and every
// step above runs against a filesystem missing files nobody will name.
func TestAStackIsHeldOnlyIfEveryLayerIs(t *testing.T) {
	t.Parallel()

	var (
		k     = core.Key{9}
		first = ir.NodeID{1}
		mid   = ir.NodeID{2}
		last  = ir.NodeID{3}
	)

	e := core.Entry{Layers: []ir.NodeID{first, mid, last}, Writer: "w"}
	ac := oneEntry{key: k, e: e}

	t.Run("every layer held is a hit", func(t *testing.T) {
		t.Parallel()

		all := someBlobs{first: true, mid: true, last: true}
		if _, ok := core.Lookup(ac, all, nil, k); !ok {
			t.Error("an entry whose every layer is held was refused")
		}
	})

	// Each position separately, because a loop that checks one of them is a
	// loop that passes a test naming only the others.
	for _, c := range []struct {
		name    string
		missing ir.NodeID
	}{
		{"the first", first},
		{"one in the middle", mid},
		{"the last", last},
	} {
		t.Run(c.name+" layer missing is a miss", func(t *testing.T) {
			t.Parallel()

			have := someBlobs{first: true, mid: true, last: true}
			delete(have, c.missing)

			if _, ok := core.Lookup(ac, have, nil, k); ok {
				t.Errorf("an entry was served with %s layer absent from the store"+
					"\n  the stack materialises without it and nothing downstream"+
					" can tell that from a complete one", c.name)
			}
		})
	}
}
