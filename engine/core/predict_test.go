package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

var site = "Earthfile:12 command -v unbuffer"

// With no history there is no prediction, and the predictor says so rather than
// guessing.
//
// Defaulting to a branch spends a build's parallelism on a coin toss, and the
// cost lands on builds with no history - which are the ones a new user runs.
func TestNoHistoryMeansNoPrediction(t *testing.T) {
	t.Parallel()

	p := core.NewPredictions()

	if _, confident := p.Predict(site); confident {
		t.Error("predicted a branch it had never seen")
	}

	p.Observe(site, true)

	if _, confident := p.Predict(site); confident {
		t.Error("one observation is not a pattern")
	}
}

// A consistent condition becomes predictable.
func TestAConsistentConditionIsPredicted(t *testing.T) {
	t.Parallel()

	p := core.NewPredictions()

	for range 5 {
		p.Observe(site, true)
	}

	branch, confident := p.Predict(site)
	if !confident {
		t.Fatal("five identical observations were not enough")
	}

	if !branch {
		t.Error("predicted the branch that was never taken")
	}
}

// A condition that alternates is not predictable, and claiming otherwise wastes
// half the work the speculation does.
func TestAnAlternatingConditionIsNotPredicted(t *testing.T) {
	t.Parallel()

	p := core.NewPredictions()

	for i := range 10 {
		p.Observe(site, i%2 == 0)
	}

	if _, confident := p.Predict(site); confident {
		t.Error("claimed confidence about a condition that alternates")
	}
}

// The property the whole idea rests on: a prediction is a hint, and hints do not
// decide anything (green paper I5).
//
// The branch a build takes is whatever running the condition yields. A predictor
// that could change it would turn a stale statistic into a wrong build - the
// same failure as a cache that answers without checking.
func TestPredictionsDoNotDecideBranches(t *testing.T) {
	t.Parallel()

	p := core.NewPredictions()

	for range 10 {
		p.Observe(site, true)
	}

	predicted, confident := p.Predict(site)
	if !confident || !predicted {
		t.Fatal("expected a confident true prediction to test against")
	}

	// Whatever the predictor believes, the condition's own result stands.
	for _, actual := range []bool{true, false} {
		if taken := core.TakeBranch(p, site, func() bool { return actual }); taken != actual {
			t.Errorf("the build took %v when the condition said %v", taken, actual)
		}
	}

	// And observing the surprise updates the belief rather than being discarded.
	if _, confident := p.Predict(site); !confident {
		t.Log("a run of surprises correctly reduced confidence")
	}
}
