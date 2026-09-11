package cli

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func observedResult(places []core.Placement, obs core.Observation) core.Result {
	return core.Result{Observation: obs, Observed: true, Placements: places}
}

// ran is a step that executed a command: the kind whose reads decide a result,
// and the kind whose silence is a gap rather than an absence.
func ran(w *skipWatch, r core.Result) { w.saw(ir.OpExec, r) }

// A build is many steps, and the record is about all of them: what any step
// read from the checkout is an input to the build, whichever step read it.
func TestAWatchGathersEveryStepsReadsAndPlacements(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one", "src/b.txt": "two"})

	var w skipWatch

	ran(&w, observedResult(placedAt(), read("/w/src/a.txt")))
	ran(&w, observedResult(nil, read("/w/src/b.txt")))

	in, err := w.hostInputs(map[string]bool{contextLayer: true}, root)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	if len(in) != 2 {
		t.Fatalf("gathered %d inputs from two steps, want 2: %v", len(in), in)
	}

	if in[0].Path != "src/a.txt" || in[1].Path != "src/b.txt" {
		t.Errorf("gathered %v", in)
	}
}

// **A step nobody watched is a step whose inputs are unknown, not one with
// none.** H2 of docs-internals/job-skipping.md: this is the gate between a job
// key and a green tick on a build nobody ran.
func TestAnUnobservedStepPoisonsTheWholeRecord(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})

	var w skipWatch

	ran(&w, observedResult(placedAt(), read("/w/src/a.txt")))
	ran(&w, core.Result{Observed: false})

	_, err := w.hostInputs(map[string]bool{contextLayer: true}, root)
	if err == nil {
		t.Error("a build with an unobserved step produced host inputs")
	}
}

// H1: one step's tracer missing something poisons the build's key, not just
// that step's.
func TestAnIncompleteStepPoisonsTheWholeRecord(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})

	missed := read("/w/src/a.txt")
	missed.Incomplete = true

	var w skipWatch

	ran(&w, observedResult(placedAt(), read("/w/src/a.txt")))
	ran(&w, observedResult(nil, missed))

	_, err := w.hostInputs(map[string]bool{contextLayer: true}, root)
	if err == nil {
		t.Error("a build with an incomplete observation produced host inputs")
	}
}

// A step that produced no layer and watched nothing - a plan node rather than a
// command - is not an unobserved step. Counting it as one would mean no build
// with a `FROM` ever earns a key.
func TestAStepWithNothingToObserveIsNotAGap(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})

	var w skipWatch

	ran(&w, observedResult(placedAt(), read("/w/src/a.txt")))

	// A FROM: it resolves an image and watches nothing, which is not a gap.
	w.saw(ir.OpImage, core.Result{Observed: false})

	_, err := w.hostInputs(map[string]bool{contextLayer: true}, root)
	if err != nil {
		t.Errorf("a step with nothing to observe was treated as a gap: %v", err)
	}
}
