package cli

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A build that copied and cannot say where refuses, and this is a false skip
// that was measured rather than imagined.
//
// The guard beside this one asks whether the build placed something from the
// checkout and read none of it. It cannot fire when there are no placements at
// all - `placedFromAContext` is false over an empty list - so a build whose
// copies were all served from cache returned an empty 𝑅 as an honest answer.
// `noteBuild` then added the Earthfile and wrote a record of one input, which
// has a shape that matches and an input that has not moved, so it skips.
//
// Measured on examples/rust-layered with the engine as it stood: a cold build
// recorded 77 inputs, a second build over the warm store recorded 1, and a
// third that edited `crates/greet/src/lib.rs` - a file the build compiles -
// printed `auto-skip: build-warm was built with these inputs before` and ran
// nothing.
//
// **Empty is not a fact about a build that copied.** It is the absence of one.
func TestACopyingBuildThatAccountsForNothingRefuses(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})
	ctx := map[string]bool{contextLayer: true}

	// The shape the bug took: a COPY served from cache, so it reports no
	// placements, and a RUN above it whose reads nothing can name.
	cached := core.StepRecord{Kind: ir.OpFile, Outcome: core.OutcomeL1Hit}

	rec := &core.Record{Steps: []core.StepRecord{
		cached, ranAndWatched(nil, read("/w/src/a.txt")),
	}}

	if _, err := hostInputsOfBuild(rec, profilesOf{}, ctx, root); err == nil {
		t.Error("a build whose COPY cannot say where it put anything derived a key" +
			"\n  its 𝑅 is empty, so the record skips on any change to any file")
	}

	// A build with no copy at all is a different thing, and still allowed: it
	// took nothing from the checkout, which σ already covers.
	none := &core.Record{Steps: []core.StepRecord{
		{Kind: ir.OpImage, Outcome: core.OutcomeMiss},
		ranAndWatched(nil, core.Observation{}),
	}}

	if _, err := hostInputsOfBuild(none, profilesOf{}, ctx, root); err != nil {
		t.Errorf("a build that copies nothing was refused: %v", err)
	}

	// And a copy that *can* account for itself still derives.
	fine := &core.Record{Steps: []core.StepRecord{
		{Kind: ir.OpFile, Outcome: core.OutcomeMiss, Placements: placedAt()},
		ranAndWatched(nil, read("/w/src/a.txt")),
	}}

	if _, err := hostInputsOfBuild(fine, profilesOf{}, ctx, root); err != nil {
		t.Errorf("a copy that reported its placements was refused: %v", err)
	}
}

// A build whose every step was cached derives the same 𝑅 as the build that ran.
//
// This is the property the whole mechanism rests on and the one nothing was
// checking: a record is written by one build and read by another, and if the
// cached build's 𝑅 is the smaller of the two, the key it writes ignores the
// difference and skips on a change to it.
//
// It regressed the moment `COPY` stopped being a *watched* kind. Requiring an
// observation of a copy was wrong, but the same predicate also gated whether a
// cached copy's profile was consulted at all - so 𝑅 quietly went from 77 inputs
// to 73 on examples/rust-layered, with nothing red. What a copy owes and what a
// copy can contribute are two questions.
func TestACachedBuildDerivesTheSameInputsAsTheBuildThatRan(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one", "src/b.txt": "two"})
	ctx := map[string]bool{contextLayer: true}

	copyClass := ir.NodeID{'c', 'p'}
	runClass := ir.NodeID{'r', 'n'}

	// The build that ran: the copy observed one path, the exec another.
	ran := &core.Record{Steps: []core.StepRecord{
		{
			Kind: ir.OpFile, Outcome: core.OutcomeMiss, Class: copyClass,
			Observed: true, Observation: read("/w/src/a.txt"), Placements: placedAt(),
		},
		{
			Kind: ir.OpExec, Outcome: core.OutcomeMiss, Class: runClass,
			Observed: true, Observation: read("/w/src/b.txt"),
		},
	}}

	// The same build, everything served from cache: placements come back with
	// the entry, reads come back from the profiles.
	cached := &core.Record{Steps: []core.StepRecord{
		{Kind: ir.OpFile, Outcome: core.OutcomeL1Hit, Class: copyClass, Placements: placedAt()},
		{Kind: ir.OpExec, Outcome: core.OutcomeL1Hit, Class: runClass},
	}}

	known := profilesOf{
		copyClass: read("/w/src/a.txt"),
		runClass:  read("/w/src/b.txt"),
	}

	wasIn, err := hostInputsOfBuild(ran, known, ctx, root)
	if err != nil {
		t.Fatalf("the build that ran refused: %v", err)
	}

	nowIn, err := hostInputsOfBuild(cached, known, ctx, root)
	if err != nil {
		t.Fatalf("the cached build refused: %v", err)
	}

	if len(wasIn) != len(nowIn) {
		t.Fatalf("the build that ran derived %d inputs and the cached one %d"+
			"\n  ran:    %v\n  cached: %v"+
			"\n  the smaller key ignores the difference and skips on a change to it",
			len(wasIn), len(nowIn), wasIn, nowIn)
	}

	for i := range wasIn {
		if wasIn[i] != nowIn[i] {
			t.Errorf("input %d: ran %+v, cached %+v", i, wasIn[i], nowIn[i])
		}
	}
}
