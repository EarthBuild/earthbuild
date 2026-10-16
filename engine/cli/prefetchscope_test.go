package cli

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// A build speculates on its own history and not on somebody else's.
//
// **The site key was not project-qualified.** `siteOf` is the diagnostic
// location plus the condition, and for a local file that location is relative -
// so `Earthfile:10 [ -e /cache ]` in one project was the same site as
// `Earthfile:10 [ -e /cache ]` in another. A store on a machine that has built
// several projects hands every one of them to every build.
//
// The cost is measured rather than assumed: a `python:3.13-slim` build was
// fetching a registry token for **alpine**, an image it never mentions, and
// moving `predictions.json` aside took the build from 3050ms to 2494ms - half a
// second of a three-second build, spent on bandwidth taken from the pull it
// actually needed (E732).
//
// It is also a correctness question and not only a speed one. One project's
// branch history was deciding what another would probably do. That is harmless
// while a prediction only selects what to *speculate* on (I5) and stops being
// harmless the moment one decides anything else.
func TestABuildSpeculatesOnlyOnItsOwnSites(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	elsewhere := t.TempDir()

	learned := core.NewPredictions()

	// The same relative location in two projects: the collision itself.
	const where = "Earthfile:10"

	mine := siteOf([]string{"test", "-e", "/here"}, where, root)
	theirs := siteOf([]string{"test", "-e", "/here"}, where, elsewhere)

	if mine == theirs {
		t.Fatalf("two projects share the site %q"+
			"\n  a condition at %s of one project is not the condition at %s of"+
			" another, and a key that cannot tell them apart lets one project's"+
			" history steer the other's speculation (E732)", mine, where, where)
	}

	for range 3 {
		recordBranch(learned, []string{"test", "-e", "/here"}, where, true, root)
		recordBranch(learned, []string{"test", "-e", "/here"}, where, true, elsewhere)
	}

	recordNeeds(learned, map[string]bool{mine: true}, []string{"mine:1"})
	recordNeeds(learned, map[string]bool{theirs: true}, []string{"theirs:1"})

	var (
		mu     sync.Mutex
		pulled []string
	)

	pull := func(_ context.Context, ref string) error {
		mu.Lock()
		defer mu.Unlock()

		pulled = append(pulled, ref)

		return nil
	}

	prefetch(context.Background(), root, learned, pull)()

	mu.Lock()
	defer mu.Unlock()

	for _, ref := range pulled {
		if ref == "theirs:1" {
			t.Errorf("a build under %s prefetched %q, which only another project ever needed"+
				"\n  pulled: %v"+
				"\n  speculation is free only when it is speculation about this"+
				" build; bytes fetched for another project are taken from the"+
				" pull this one is waiting on (E732)",
				filepath.Base(root), ref, pulled)
		}
	}

	if len(pulled) != 1 || pulled[0] != "mine:1" {
		t.Errorf("this build's own prediction was not prefetched: pulled %v, want [mine:1]"+
			"\n  scoping speculation to the build must not switch it off", pulled)
	}
}
