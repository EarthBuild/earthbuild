package cli

import (
	"testing"
)

// One build opens one action cache.
//
// Two places opened one: the lazy engine that answers a condition the
// interpreter cannot decide, and the build that follows it. Both pointed at the
// same directory, so entries were shared and I9 held on disk - os.Link is
// atomic whoever calls it - and what did *not* survive was the conflict record,
// which lives in the object. A key that claimed two results while a condition
// was being probed was refused and then reported by nobody.
//
// That is the un-updated sibling, which this branch has now hit in five
// distinct places: a rule implemented once and consulted twice. The compiler
// cannot see it and neither can a unit test, because each half is correct
// alone. Counting the constructor is the cheapest thing that can.
//
// If a second cache genuinely becomes necessary, this test is the place to say
// so - and the conflict reporting has to take the union before it goes.
func TestOneBuildOpensOneActionCache(t *testing.T) {
	t.Parallel()

	found, err := nonTestFilesContaining(".", "cache.Open(")
	if err != nil {
		t.Fatal(err)
	}

	total := 0
	for _, n := range found {
		total += n
	}

	if total != 1 {
		t.Errorf("the package opens the action cache %d times, in %v"+
			"\n  each object keeps its own conflict record, so a refused rewrite"+
			"\n  seen by one of them is reported by neither", total, found)
	}
}
