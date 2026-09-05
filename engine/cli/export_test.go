package cli

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// SeedPrediction writes a history in which one condition has consistently gone
// one way, for a test in the external package to build against.
//
// Through the engine's own recorder and writer rather than by composing a file.
// A hand-written fixture is a second implementation of a format, and the last
// one of those turned three assertions into three green SKIP lines because its
// key did not match what the encoder produces (E120).
//
// `times` is how often the condition went that way: `Predict` is confident only
// once a site has been consistent, so a test that wants a *confident* wrong
// prediction has to say how confident.
func SeedPrediction(t *testing.T, dir string, cond []string, where string, taken bool, times int) {
	t.Helper()

	p := core.NewPredictions()

	for range times {
		recordBranch(p, cond, where, taken, "")
	}

	err := savePredictions(dir, p)
	if err != nil {
		t.Fatalf("seed a prediction history: %v", err)
	}

	// Asserted, not assumed: a seed that does not make the predictor confident
	// leaves the test asserting that an *absent* prediction changes nothing,
	// which is true and is not the claim.
	// Read back through the loader a build uses, not from the object just
	// written: what matters is that the *file* makes the next build confident,
	// and an in-memory check would pass for a history the loader cannot read.
	back, err := loadPredictions(dir)
	if err != nil {
		t.Fatalf("the seeded history does not load: %v", err)
	}

	branch, confident := back.Predict(siteOf(cond, where, ""))
	if !confident || branch != taken {
		t.Fatalf("the seeded history is not confident about %v: (%v, %v)"+
			"\n  a test built on it would assert that an *absent* prediction changes"+
			"\n  nothing, which is true and is not the claim", cond, branch, confident)
	}
}
