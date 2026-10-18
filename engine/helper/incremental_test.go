package helper

import (
	"reflect"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A cache whose units never change is exported once and topped up thereafter.
//
// **The cost this design otherwise pays on every step.** An export reads and
// frames every unit in the mount, so a warm 88,000-unit cache is re-read to
// learn what it already knew - and a step that touched a hundred of them pays
// for the other 87,900. The units dedupe in 𝔅, being the same bytes under the
// same names, so it costs work rather than space, which is the kind of waste
// nothing complains about.
func TestOnlyNewUnitsAreExportedWhereUnitsAreImmutable(t *testing.T) {
	t.Parallel()

	prev := Map{"a": ir.DigestOf([]byte("A")), "b": ir.DigestOf([]byte("B"))}

	want, keep := Needed(prev, []string{"a", "b", "c"}, true)

	if !reflect.DeepEqual(want, []string{"c"}) {
		t.Errorf("exporting %v, want only the key the last map did not name", want)
	}

	if len(keep) != 2 || keep["a"] != prev["a"] || keep["b"] != prev["b"] {
		t.Errorf("carried %v forward, want both entries the last map already had", keep)
	}
}

// Where a unit may change, everything is exported.
//
// **npm is why this is a property and not an assumption.** A cacache bucket is
// append-only and holds several records, so a key present in both indexes can
// have gained one - and a key set that compares equal is then a cache that has
// changed. Skipping on key equality would file a map naming last build's bytes
// for a key whose unit has grown, and a peer stocking from it would import a
// record set short of what the sender holds.
//
// Only the helper knows which it is, which is the whole reason a helper exists.
func TestEverythingIsExportedWhereAUnitMayChange(t *testing.T) {
	t.Parallel()

	prev := Map{"a": ir.DigestOf([]byte("A"))}

	want, keep := Needed(prev, []string{"a", "b"}, false)

	if !reflect.DeepEqual(want, []string{"a", "b"}) {
		t.Errorf("exporting %v, want every key: a unit under a key this already"+
			" names may have grown since", want)
	}

	if len(keep) != 0 {
		t.Errorf("carried %v forward from a map that may be stale", keep)
	}
}

// A unit the cache no longer holds is not carried forward.
//
// The map would otherwise grow monotonically and never forget: a tool that
// prunes its own cache would leave this naming units nobody has, and every later
// map would inherit them. Bounded by the index, which is the set of things
// actually here.
func TestAPrunedUnitIsNotCarriedForward(t *testing.T) {
	t.Parallel()

	prev := Map{"a": ir.DigestOf([]byte("A")), "gone": ir.DigestOf([]byte("G"))}

	want, keep := Needed(prev, []string{"a"}, true)

	if len(want) != 0 {
		t.Errorf("exporting %v, want nothing: the only key here is already named", want)
	}

	if _, held := keep["gone"]; held {
		t.Error("a key the cache no longer holds was carried into the new map" +
			"\n  a peer would be told to fetch a unit this machine cannot serve")
	}
}

// With no previous map, everything is new.
func TestWithNoPreviousMapEverythingIsExported(t *testing.T) {
	t.Parallel()

	want, keep := Needed(nil, []string{"b", "a"}, true)

	if !reflect.DeepEqual(want, []string{"a", "b"}) {
		t.Errorf("exporting %v, want every key sorted", want)
	}

	if len(keep) != 0 {
		t.Errorf("carried %v forward from no map at all", keep)
	}
}
