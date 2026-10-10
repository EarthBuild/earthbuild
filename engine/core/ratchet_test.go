package core_test

import (
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// A prefetch mask stops naming what stopped being needed.
//
// Green paper §A.3: *"Masks are unioned on consultation and extended on miss:
// an unpredicted access demand-faults normally and is added. Extension alone is
// a ratchet that converges on the whole layer, so each entry carries a use
// count and is dropped after 𝑁 unused consultations."*
//
// `Needed` implemented the first sentence and not the second. It merges refs
// into the set and removes nothing, ever, so an Earthfile whose branch used
// `node:18` and now uses `node:22` prefetches both - and after ten base-image
// bumps, ten images are pulled before every build. §A.4 names that outcome:
// *"precision prevents degeneration into eager transfer"*, and there was no
// mechanism keeping precision at all.
//
// A consultation is a build: `recordNeeds` calls `Needed` once per decided site,
// with everything that build wanted. So an entry absent from a call is an entry
// that build did not want.
func TestAPrefetchMaskDropsWhatStoppedBeingNeeded(t *testing.T) {
	t.Parallel()

	const site = "./Earthfile:12 IF command -v node"

	p := core.NewPredictions()

	p.Needed(site, true, []string{"node:18", testBaseImage})

	if got := p.Needs(site, true); len(got) != 2 {
		t.Fatalf("the first consultation recorded %v", got)
	}

	// The branch now uses a different base. Every build from here wants
	// node:22, and none wants node:18.
	for range core.MaskIdleLimit {
		p.Needed(site, true, []string{"node:22", testBaseImage})
	}

	got := p.Needs(site, true)

	if slices.Contains(got, "node:18") {
		t.Errorf("after %d builds that did not want it, the mask still names"+
			" node:18: %v\n  extension alone converges on every image the"+
			" project has ever used", core.MaskIdleLimit, got)
	}

	// And the extension half still works, or the drop has eaten the feature.
	if !slices.Contains(got, "node:22") {
		t.Errorf("the newly needed image is not in the mask: %v", got)
	}

	if !slices.Contains(got, testBaseImage) {
		t.Errorf("an image needed every time was dropped: %v", got)
	}
}

// One unusual build does not discard a good entry.
//
// The limit is not one. A branch that occasionally takes a shortcut - a cache
// hit that skips a stage, a conditional inside a conditional - would otherwise
// throw away a mask that is right almost always, and the next build pays the
// full cold-fetch it was avoiding.
func TestOneBuildThatSkippedAnImageDoesNotDropIt(t *testing.T) {
	t.Parallel()

	const site = "./Earthfile:3 IF [ -f /flag ]"

	p := core.NewPredictions()

	p.Needed(site, false, []string{"golang:1.24", testBaseImage})
	p.Needed(site, false, []string{testBaseImage}) // one build did not need it

	if !slices.Contains(p.Needs(site, false), "golang:1.24") {
		t.Error("one consultation without an image dropped it, so a mask is" +
			" only as good as the least typical build")
	}
}

// Being needed again resets the count.
//
// Otherwise the counter is a lifetime total rather than a run of idleness, and
// an image needed by every second build is dropped on schedule regardless of
// how useful it is.
func TestNeedingAnImageAgainResetsItsIdleCount(t *testing.T) {
	t.Parallel()

	const site = "./Earthfile:9 IF true"

	p := core.NewPredictions()

	p.Needed(site, true, []string{"rust:1.90", testBaseImage})

	// Idle, then wanted, repeatedly - never idle for MaskIdleLimit in a row.
	for range core.MaskIdleLimit * 3 {
		for range core.MaskIdleLimit - 1 {
			p.Needed(site, true, []string{testBaseImage})
		}

		p.Needed(site, true, []string{"rust:1.90", testBaseImage})
	}

	if !slices.Contains(p.Needs(site, true), "rust:1.90") {
		t.Error("an image wanted every few builds was dropped, so the count is" +
			" a lifetime total rather than a run of idleness")
	}
}

// The counts survive a build, or the ratchet never releases.
//
// A consultation is a build and the drop happens after several, so a count that
// reset when the process exited would never reach the limit. This is the half
// that is easy to implement and easy to leave out, because every unit test
// passes without it.
func TestIdleCountsSurviveTheProcess(t *testing.T) {
	t.Parallel()

	const site = "./Earthfile:1 IF x"

	first := core.NewPredictions()
	first.Needed(site, true, []string{"gone:1", "kept:1"})

	for range core.MaskIdleLimit {
		// Each "build" is a fresh Predictions restored from what was saved,
		// which is what the engine actually does.
		next := core.NewPredictions()
		next.RestoreNeeds(first.NeedsSnapshot())
		next.RestoreIdle(first.IdleSnapshot())
		next.Needed(site, true, []string{"kept:1"})

		first = next
	}

	if slices.Contains(first.Needs(site, true), "gone:1") {
		t.Errorf("the idle counts did not survive being saved and reloaded,"+
			" so nothing is ever dropped in a real build: %v", first.Needs(site, true))
	}
}
