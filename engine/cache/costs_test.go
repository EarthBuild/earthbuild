package cache_test

import (
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
)

// What a kind of step costs is remembered between builds.
//
// **The measurement the fleet has never had.** `Hints.EstimatedSeconds` has
// been declared, encoded and decoded since the protocol was written, and
// nothing ever set it: placement's only input about cost is `Bytes`, the size
// of a step's inputs. So a base worth shipping for a ten-minute compile and one
// worth keeping for a two-second one are priced identically, against a
// fleet-wide *average* step (`Rate.Slots`).
//
// Keyed by class rather than by chain key, which is what makes it survive an
// edit: a class is a prediction key, so `go build ./...` is the same kind of
// step whether or not a source file changed. A key that changed with the source
// would have history for nothing.
func TestACostIsRememberedBetweenBuilds(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	class := core.Key{1, 2, 3}

	c, err := cache.OpenCosts(dir)
	if err != nil {
		t.Fatal(err)
	}

	c.Put(class, 90*time.Second)

	// A second opening is the next build: nothing is carried in memory.
	again, err := cache.OpenCosts(dir)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := again.Get(class)
	if !ok {
		t.Fatal("a cost filed by one build is not there for the next")
	}

	if got != 90*time.Second {
		t.Errorf("remembered %v, want 90s", got)
	}
}

// A kind of step nobody has run has no cost, and says so.
//
// **Absent is not zero.** Zero would read as "instant", and a step priced at
// nothing is one no transfer could ever be worth - the inverted answer
// `Rate.Slots` already refuses to give for an unstated size.
func TestAnUnknownClassHasNoCost(t *testing.T) {
	t.Parallel()

	c, err := cache.OpenCosts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := c.Get(core.Key{9}); ok {
		t.Error("a class nobody has run reported a cost")
	}
}

// The latest run wins, rather than the first.
//
// A machine that gets faster, a step that grows: the useful answer is what it
// costs now. An average over history would take a build to forget a change that
// a single run already knows about.
func TestTheLatestRunIsWhatIsRemembered(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	class := core.Key{4}

	c, err := cache.OpenCosts(dir)
	if err != nil {
		t.Fatal(err)
	}

	c.Put(class, 10*time.Second)
	c.Put(class, 2*time.Second)

	if got, _ := c.Get(class); got != 2*time.Second {
		t.Errorf("remembered %v, want the most recent run (2s)", got)
	}
}

// A step that took no measurable time is not filed.
//
// Nothing is learned from it and it would displace a real measurement: a step
// the backend could not time reports zero, and zero is "could not say" rather
// than "instant" (E467).
func TestAnUnmeasuredStepIsNotFiled(t *testing.T) {
	t.Parallel()

	c, err := cache.OpenCosts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	class := core.Key{5}
	c.Put(class, 3*time.Second)
	c.Put(class, 0)

	if got, _ := c.Get(class); got != 3*time.Second {
		t.Errorf("an unmeasured run overwrote a real one: %v", got)
	}
}
