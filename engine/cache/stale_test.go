package cache_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// An entry naming a layer the store no longer holds is replaced.
//
// **Otherwise the key is poisoned for ever.** `Lookup` refuses an entry whose
// result is absent - "a claim whose result is not present is not usable,
// however well signed" - and `Put` leaves an existing entry alone, so a store
// that loses a layer leaves a step that can never hit again. It missed, it
// reran, it published, and the publish was dropped on the floor because
// something was already there.
//
// A step whose delta is empty is where this bites hardest: it is the cheapest
// step to rerun and the one an author is least likely to suspect.
func TestAnEntryWhoseLayerIsGoneIsReplaced(t *testing.T) {
	t.Parallel()

	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	lost, kept := id(t, 1), id(t, 2)

	// Only `kept` is in the store, which is the state after a collection - or
	// after a sandbox lost what it had not written down.
	c.Held = func(e core.Entry) bool { return e.Layer == kept }

	var k core.Key

	c.Put(k, core.Entry{Layer: lost, Writer: "earthbuild"})
	c.Put(k, core.Entry{Layer: kept, Writer: "earthbuild"})

	got, ok := c.Get(k)
	if !ok {
		t.Fatal("the entry went away entirely")
	}

	if got.Layer != kept {
		t.Errorf("the cache still names the lost layer %s", got.Layer)
	}
}

// A held entry is still left alone, which is I9 and the reason two builders
// racing on one key do not fight.
func TestAHeldEntryIsNotReplaced(t *testing.T) {
	t.Parallel()

	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	first, second := id(t, 1), id(t, 2)

	c.Held = func(core.Entry) bool { return true }

	var k core.Key

	c.Put(k, core.Entry{Layer: first, Writer: "earthbuild"})
	c.Put(k, core.Entry{Layer: second, Writer: "earthbuild"})

	got, _ := c.Get(k)
	if got.Layer != first {
		t.Errorf("a held entry was modified in place: %s", got.Layer)
	}

	// And the disagreement is still recorded, which is what makes a
	// non-reproducible step visible rather than merely tolerated.
	if c.ConflictCount() == 0 {
		t.Error("two claims on one key were not recorded as a conflict")
	}
}

// With nothing to ask, the old rule stands: an entry already here is left
// alone. A cache that cannot see the store must not throw away claims on the
// suspicion that they are dead.
func TestWithoutTheQuestionNothingIsReplaced(t *testing.T) {
	t.Parallel()

	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	first, second := id(t, 1), id(t, 2)

	var k core.Key

	c.Put(k, core.Entry{Layer: first, Writer: "earthbuild"})
	c.Put(k, core.Entry{Layer: second, Writer: "earthbuild"})

	got, _ := c.Get(k)
	if got.Layer != first {
		t.Errorf("an entry was replaced with no way to know the first was lost: %s", got.Layer)
	}
}

func id(t *testing.T, b byte) ir.NodeID {
	t.Helper()

	var raw [32]byte
	raw[0] = b

	return ir.NodeID(raw)
}
