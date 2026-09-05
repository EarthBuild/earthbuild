package cli

import (
	"context"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// TestAPrefetchCancelsWhatItSpeculatedOn.
//
// **Speculation that has not paid off by the end of a build cannot help it.**
// The prefetch pulls what the predictions say some site may need, and the
// function it returns was `wg.Wait` - so a build that needed none of them
// waited for all of them before exiting.
//
// Measured: a warm no-op build takes 380ms with a predictions file present and
// 37ms with it moved aside, and the difference is that wait (E727). Ten times
// the build, spent fetching images the build never asked for - including, on
// this machine, an amd64 digest an arm64 build cannot use.
//
// Cancelled rather than left running, which is what the wait was for in the
// first place: a pull must not outlive the build that speculated on it.
func TestAPrefetchCancelsWhatItSpeculatedOn(t *testing.T) {
	t.Parallel()

	learned := core.NewPredictions()
	site := siteOf([]string{"true"}, "Earthfile:1", "")

	// Three consistent decisions, because Predict wants at least two and a
	// three-quarters majority before it will speculate on a site.
	for range 3 {
		recordBranch(learned, []string{"true"}, "Earthfile:1", true, "")
	}

	recordNeeds(learned, map[string]bool{site: true}, []string{"alpine:3.22"})

	var (
		mu     sync.Mutex
		handed []context.Context
	)

	pull := func(ctx context.Context, _ string) error {
		mu.Lock()
		defer mu.Unlock()

		handed = append(handed, ctx)

		return nil
	}

	done := prefetch(context.Background(), "", learned, pull)
	done()

	mu.Lock()
	defer mu.Unlock()

	if len(handed) == 0 {
		t.Fatal("no speculation happened, so this test guards nothing - check" +
			" what Predict wants before it is confident")
	}

	for i, ctx := range handed {
		if ctx.Err() == nil {
			t.Errorf("pull %d was given a context still live after the build"+
				" returned, so a speculative fetch can outlast the build that"+
				" wanted it", i)
		}
	}
}
