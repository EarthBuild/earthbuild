package cli

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// ranAndWatched is a step that executed and whose observation the scheduler
// judged usable.
func ranAndWatched(places []core.Placement, obs core.Observation) core.StepRecord {
	return core.StepRecord{
		Outcome: core.OutcomeMiss, Observation: obs, Observed: true, Placements: places,
	}
}

// A build is many steps, and the record is about all of them: what any step
// read from the checkout is an input to the build, whichever step read it.
func TestAWatchGathersEveryStepsReadsAndPlacements(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one", "src/b.txt": "two"})

	rec := &core.Record{Steps: []core.StepRecord{
		ranAndWatched(placedAt(), read("/w/src/a.txt")),
		ranAndWatched(nil, read("/w/src/b.txt")),
	}}

	in, err := hostInputsOfBuild(rec, map[string]bool{contextLayer: true}, root)
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

	rec := &core.Record{Steps: []core.StepRecord{
		ranAndWatched(placedAt(), read("/w/src/a.txt")),
		{Outcome: core.OutcomeMiss, Observed: false},
	}}

	_, err := hostInputsOfBuild(rec, map[string]bool{contextLayer: true}, root)
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

	rec := &core.Record{Steps: []core.StepRecord{
		ranAndWatched(placedAt(), read("/w/src/a.txt")),
		ranAndWatched(nil, missed),
	}}

	_, err := hostInputsOfBuild(rec, map[string]bool{contextLayer: true}, root)
	if err == nil {
		t.Error("a build with an incomplete observation produced host inputs")
	}
}

// A step served from cache did not run, so it watched nothing. That is not a
// gap in the sense that matters - but it does mean this build cannot refresh
// the record, which TestAPartialRebuildDoesNotRefreshTheRecord pins.
func TestACachedStepIsNotAGap(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})

	rec := &core.Record{Steps: []core.StepRecord{
		ranAndWatched(placedAt(), read("/w/src/a.txt")),
		// A step served from cache: it did not run, so it watched nothing, and
		// what it would have read is whatever the key that hit already covered.
		{Outcome: core.OutcomeL1Hit, Observed: false},
	}}

	_, err := hostInputsOfBuild(rec, map[string]bool{contextLayer: true}, root)
	if err != nil {
		t.Errorf("a step with nothing to observe was treated as a gap: %v", err)
	}
}

// **A build that hit cache did not observe what those steps would have read**,
// so what it gathered is a subset of the build's inputs - and a subset is
// exactly the shape that skips on a change nobody accounted for. Such a build
// leaves the existing record alone.
func TestAPartialRebuildDoesNotRefreshTheRecord(t *testing.T) {
	t.Parallel()

	all := &core.Record{Steps: []core.StepRecord{
		ranAndWatched(placedAt(), read("/w/src/a.txt")),
		ranAndWatched(nil, read("/w/src/b.txt")),
	}}

	if !refreshable(all) {
		t.Error("a build where every step ran cannot refresh the record")
	}

	partial := &core.Record{Steps: []core.StepRecord{
		ranAndWatched(placedAt(), read("/w/src/a.txt")),
		{Outcome: core.OutcomeL2Hit},
	}}

	if refreshable(partial) {
		t.Error("a build with a cached step refreshed the record")
	}

	if refreshable(nil) || refreshable(&core.Record{}) {
		t.Error("a build with no steps refreshed the record")
	}
}

// A step that ran but whose output was not captured still read what it read.
func TestAnUncapturedStepStillCounts(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})

	uncaptured := ranAndWatched(placedAt(), read("/w/src/a.txt"))
	uncaptured.Outcome = core.OutcomeUncaptured

	rec := &core.Record{Steps: []core.StepRecord{uncaptured}}

	in, err := hostInputsOfBuild(rec, map[string]bool{contextLayer: true}, root)
	if err != nil || len(in) != 1 {
		t.Errorf("an uncaptured step gave %v, %v", in, err)
	}

	// And an uncaptured step that was not watched is still a gap.
	blind := core.StepRecord{Outcome: core.OutcomeUncaptured}
	if gapIn(&core.Record{Steps: []core.StepRecord{blind}}) == "" {
		t.Error("an uncaptured step that watched nothing is not a gap")
	}
}

// **A build that copied from the checkout and read none of it is suspicious.**
//
// It is the shape a broken path mapping takes: the tracer reports reads under
// one spelling, the placements record another, nothing matches, and 𝑅 comes
// back empty. An empty 𝑅 with a matching shape *skips* - so the failure of the
// rewrite is a build that never runs, which is the one outcome this must not
// produce.
//
// It can also be honest: a build that copies a tree and reads nothing from it.
// That build is rare, and refusing it costs a rebuild; believing a broken
// mapping costs correctness. The asymmetry decides it.
func TestAContextCopiedAndNeverReadIsRefused(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})

	// Placed from the context, and the reads are of somewhere else entirely -
	// which is what a mapping that agrees with nothing looks like.
	elsewhere := &core.Record{Steps: []core.StepRecord{
		ranAndWatched(placedAt(), read("/somewhere/else.txt")),
	}}

	_, err := hostInputsOfBuild(elsewhere, map[string]bool{contextLayer: true}, root)
	if err == nil {
		t.Error("a build that placed a context and mapped no read produced inputs")
	}

	// A build that placed nothing from a context is not suspicious: it has no
	// checkout inputs because it has no checkout copies.
	none := &core.Record{Steps: []core.StepRecord{
		ranAndWatched(nil, read("/etc/alpine-release")),
	}}

	got, err := hostInputsOfBuild(none, map[string]bool{contextLayer: true}, root)
	if err != nil || len(got) != 0 {
		t.Errorf("a build with no context copies gave %v, %v", got, err)
	}
}
