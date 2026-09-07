package core_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A stale prediction says which path disagreed.
//
// `Consistent` returned a bool, so a build whose L2 never hits reports
// `1 of 3 predictions stale` and nothing else. That is a count without a cause:
// it says the tier is being invalidated and not by what, and the only way
// forward is to guess and measure.
//
// **Which is exactly what it cost when the tier went live.** Every copy's
// prediction was stale because the chain walk recorded `/`, whose digest
// carries mode and extended attributes that differ between two base images
// (E125). The engine knew the path that disagreed at the moment it refused, and
// threw it away.
//
// The same shape as first-divergence reporting (B.4), which this engine already
// does for chain keys: *"keyed on 42 inputs; 1 changed"* and then the input. A
// prediction is a claim about inputs and deserves the same answer.
func TestAStalePredictionNamesThePathThatDisagreed(t *testing.T) {
	t.Parallel()

	base := fakeBase{files: map[string]ir.NodeID{
		"/usr/include/stdio.h": digest(1),
		"/w":                   digest(2),
	}}

	t.Run("a read whose digest moved", func(t *testing.T) {
		t.Parallel()

		obs := core.Observation{Reads: map[string]ir.NodeID{
			"/usr/include/stdio.h": digest(1),
			"/w":                   digest(9),
		}}

		why := core.WhyStale(obs, base)
		if why == "" {
			t.Fatal("a prediction that does not describe the base was called consistent")
		}

		if !strings.Contains(why, "/w") {
			t.Errorf("the reason does not name the path that moved: %q", why)
		}

		// And not the one that did not, or the message is a list of everything
		// the step read - which is the count-without-a-cause problem restated
		// at greater length.
		if strings.Contains(why, "stdio.h") {
			t.Errorf("the reason names a path that still agrees: %q", why)
		}
	})

	t.Run("a path that was absent and now exists", func(t *testing.T) {
		t.Parallel()

		obs := core.Observation{Negative: []string{"/w"}}

		why := core.WhyStale(obs, base)
		if !strings.Contains(why, "/w") {
			t.Errorf("the reason does not name the path that appeared: %q", why)
		}

		// The direction matters: "it exists now" and "its contents changed"
		// send a reader to different places.
		if !strings.Contains(why, "exists") && !strings.Contains(why, "appeared") {
			t.Errorf("the reason does not say the path appeared: %q", why)
		}
	})

	t.Run("a listing that changed", func(t *testing.T) {
		t.Parallel()

		obs := core.Observation{Listings: map[string]ir.NodeID{testIncludeDir: digest(5)}}

		why := core.WhyStale(obs, base)
		if !strings.Contains(why, testIncludeDir) {
			t.Errorf("the reason does not name the directory: %q", why)
		}
	})

	t.Run("a prediction that still holds has no reason", func(t *testing.T) {
		t.Parallel()

		obs := core.Observation{Reads: map[string]ir.NodeID{"/w": digest(2)}}

		if why := core.WhyStale(obs, base); why != "" {
			t.Errorf("a consistent prediction produced a reason: %q", why)
		}
	})

	// Deterministic, because a reason that names a different path on each run
	// of one build is a reason nobody can quote in a bug report - and map
	// iteration order is the classic way to produce one.
	t.Run("the same disagreement is named the same way", func(t *testing.T) {
		t.Parallel()

		obs := core.Observation{Reads: map[string]ir.NodeID{
			"/a": digest(9), "/b": digest(9), "/c": digest(9),
		}}

		first := core.WhyStale(obs, fakeBase{files: map[string]ir.NodeID{
			"/a": digest(1), "/b": digest(1), "/c": digest(1),
		}})

		for range 20 {
			again := core.WhyStale(obs, fakeBase{files: map[string]ir.NodeID{
				"/a": digest(1), "/b": digest(1), "/c": digest(1),
			}})
			if again != first {
				t.Fatalf("two runs blamed different paths:\n  %q\n  %q", first, again)
			}
		}
	})
}

// The two halves of one question never disagree.
//
// `Consistent` says yes or no and `WhyStale` says why not. They are two answers
// to one question, and this session has spent a fortnight on values that were
// two implementations of one rule - so `Consistent` is now `WhyStale(…) == ""`
// and this holds the property against the day somebody separates them again for
// speed.
//
// Randomised over the shapes that matter rather than a fixture, because the
// interesting disagreements are at the boundaries: a path in one and not the
// other, an empty observation, a base that has nothing.
func TestConsistentAndWhyStaleAgree(t *testing.T) {
	t.Parallel()

	paths := []string{"/a", "/b", "/usr", "/w"}

	for i := range 1 << len(paths) {
		obs := core.Observation{
			Reads:    map[string]ir.NodeID{},
			Listings: map[string]ir.NodeID{},
		}
		files := map[string]ir.NodeID{}

		for j, p := range paths {
			switch {
			case i&(1<<j) != 0:
				obs.Reads[p] = digest(1)
				files[p] = digest(1) // agrees
			case i%3 == 0:
				obs.Reads[p] = digest(1)
				files[p] = digest(2) // disagrees
			case i%3 == 1:
				obs.Negative = append(obs.Negative, p)
				files[p] = digest(3) // was absent, now present
			default:
				obs.Listings[p] = digest(4) // the base has no listing at all
			}
		}

		base := fakeBase{files: files}

		if got, why := core.Consistent(obs, base), core.WhyStale(obs, base); got != (why == "") {
			t.Fatalf("case %d: Consistent=%v but WhyStale=%q", i, got, why)
		}
	}
}
