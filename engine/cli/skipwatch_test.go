package cli

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ranAndWatched is a step that executed and whose observation the scheduler
// judged usable.
func ranAndWatched(places []core.Placement, obs core.Observation) core.StepRecord {
	return core.StepRecord{
		Kind: ir.OpExec, Outcome: core.OutcomeMiss,
		Observation: obs, Observed: true, Placements: places,
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

	in, err := hostInputsOfBuild(rec, profilesOf{}, map[string]bool{contextLayer: true}, root)
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
		{Kind: ir.OpExec, Outcome: core.OutcomeMiss, Observed: false},
	}}

	_, err := hostInputsOfBuild(rec, profilesOf{}, map[string]bool{contextLayer: true}, root)
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

	_, err := hostInputsOfBuild(rec, profilesOf{}, map[string]bool{contextLayer: true}, root)
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

	_, err := hostInputsOfBuild(rec, profilesOf{}, map[string]bool{contextLayer: true}, root)
	if err != nil {
		t.Errorf("a step with nothing to observe was treated as a gap: %v", err)
	}
}

func TestAnUncapturedStepStillCounts(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})

	uncaptured := ranAndWatched(placedAt(), read("/w/src/a.txt"))
	uncaptured.Outcome = core.OutcomeUncaptured

	rec := &core.Record{Steps: []core.StepRecord{uncaptured}}

	in, err := hostInputsOfBuild(rec, profilesOf{}, map[string]bool{contextLayer: true}, root)
	if err != nil || len(in) != 1 {
		t.Errorf("an uncaptured step gave %v, %v", in, err)
	}

	// And an uncaptured step that was not watched is still a gap.
	blind := core.StepRecord{Kind: ir.OpExec, Outcome: core.OutcomeUncaptured}
	if gapIn(&core.Record{Steps: []core.StepRecord{blind}}, profilesOf{}) == "" {
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

	_, err := hostInputsOfBuild(elsewhere, profilesOf{}, map[string]bool{contextLayer: true}, root)
	if err == nil {
		t.Error("a build that placed a context and mapped no read produced inputs")
	}

	// A build that placed nothing from a context is not suspicious: it has no
	// checkout inputs because it has no checkout copies.
	none := &core.Record{Steps: []core.StepRecord{
		ranAndWatched(nil, read("/etc/alpine-release")),
	}}

	got, err := hostInputsOfBuild(none, profilesOf{}, map[string]bool{contextLayer: true}, root)
	if err != nil || len(got) != 0 {
		t.Errorf("a build with no context copies gave %v, %v", got, err)
	}
}

// **A step that ran and had nothing to watch is not a step that was not
// watched.** A `FROM` pulls an image; a local context is staged. Both execute,
// both observe nothing, and neither hides a read - so treating them as gaps
// means no build with a base image ever earns a key, which is every build.
//
// The outcome cannot tell them apart, because both ran. Only the kind can, and
// this is the test that made StepRecord carry one: the end-to-end run refused
// every record with "Earthfile:4 ran and was not watched", and Earthfile:4 was
// the FROM.
func TestAStepWithNothingToWatchIsNotAGap(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})

	pulled := core.StepRecord{Kind: ir.OpImage, Outcome: core.OutcomeMiss, Observed: false}
	staged := core.StepRecord{Kind: ir.OpLocal, Outcome: core.OutcomeMiss, Observed: false}

	rec := &core.Record{Steps: []core.StepRecord{
		pulled, staged, ranAndWatched(placedAt(), read("/w/src/a.txt")),
	}}

	got, err := hostInputsOfBuild(rec, profilesOf{}, map[string]bool{contextLayer: true}, root)
	if err != nil {
		t.Fatalf("a FROM that pulled an image was treated as a gap: %v", err)
	}

	if len(got) != 1 {
		t.Errorf("gathered %v", got)
	}

	// Such a build still records: nothing was hidden.
	if why := gapIn(rec, profilesOf{}); why != "" {
		t.Errorf("a build whose FROM pulled an image was refused: %s", why)
	}

	// And a RUN that ran unwatched is still a gap.
	blind := &core.Record{Steps: []core.StepRecord{
		pulled, {Kind: ir.OpExec, Outcome: core.OutcomeMiss, Observed: false},
	}}

	if gapIn(blind, profilesOf{}) == "" {
		t.Error("a RUN that ran unwatched is not a gap")
	}
}

func TestACachedStepsReadsComeFromItsProfile(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one", "src/b.txt": "two"})

	cached := core.StepRecord{
		Seq: 9, Kind: ir.OpExec, Outcome: core.OutcomeL1Hit,
		Class: ir.NodeID{'c', 'l', 's'},
	}

	rec := &core.Record{Steps: []core.StepRecord{
		{Seq: 1, Kind: ir.OpFile, Outcome: core.OutcomeMiss, Observed: true, Placements: placedAt()},
		cached,
	}}

	known := profilesOf{cached.Class: read("/w/src/b.txt")}

	got, err := hostInputsOfBuild(rec, known, map[string]bool{contextLayer: true}, root)
	if err != nil {
		t.Fatalf("a cached step with a profile refused the key: %v", err)
	}

	if len(got) != 1 || got[0].Path != "src/b.txt" {
		t.Fatalf("the cached step's reads were not recovered: %v", got)
	}

	if got[0].Digest == gone.String() {
		t.Error("the recovered path was not re-read from the checkout")
	}
}

// A cached step nobody has a profile for still blocks: its reads are unknown,
// and unknown is not empty.
func TestACachedStepWithNoProfileStillBlocks(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})

	rec := &core.Record{Steps: []core.StepRecord{
		{Seq: 1, Kind: ir.OpFile, Outcome: core.OutcomeMiss, Observed: true, Placements: placedAt()},
		{Seq: 9, Kind: ir.OpExec, Outcome: core.OutcomeL1Hit, Class: ir.NodeID{'x'}},
	}}

	_, err := hostInputsOfBuild(rec, profilesOf{}, map[string]bool{contextLayer: true}, root)
	if err == nil {
		t.Error("a cached step nobody has a profile for did not block")
	}
}

// profilesOf is what the scheduler keeps, as a map.
type profilesOf map[ir.NodeID]core.Observation

func (p profilesOf) Get(class core.Key) (core.Observation, bool) {
	obs, ok := p[class]

	return obs, ok
}

func (p profilesOf) Put(class core.Key, obs core.Observation) { p[class] = obs }
