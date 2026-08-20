package cli

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// puller records what it was asked to fetch.
type puller struct {
	mu   sync.Mutex
	refs []string
}

func (p *puller) pull(_ context.Context, ref string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.refs = append(p.refs, ref)

	return nil
}

func (p *puller) got() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	sort.Strings(p.refs)

	return strings.Join(p.refs, ",")
}

// What a predicted branch needed last time is fetched before it is asked for.
//
// This is the freely-speculable tier doing something useful. The images a
// branch goes on to need are the expensive part of reaching it - a pull is
// network-bound and nothing else in the build can proceed during it - and they
// can be moved before the condition that selects the branch has even been run.
// Being wrong costs bandwidth; being right takes a pull off the critical path.
func TestAConfidentPredictionPrefetchesWhatThatBranchNeeded(t *testing.T) {
	t.Parallel()

	learned := core.NewPredictions()

	const site = "Earthfile:12 command -v unbuffer"

	for range 4 {
		learned.Observe(site, true)
	}

	learned.Needed(site, true, []string{testBaseImage, "golang:1.26"})
	learned.Needed(site, false, []string{"never-taken:latest"})

	p := &puller{}

	prefetch(context.Background(), learned, p.pull)()

	// The branch it expects, and not the one it does not.
	if got := p.got(); got != testTwoImages {
		t.Errorf("fetched %q, want what the predicted branch needed", got)
	}
}

// Without confidence, nothing is fetched.
//
// A site seen once is not a pattern, and pulling an image on a coin toss spends
// a new user's bandwidth to help a build that has no history to learn from -
// which is the one build that cannot benefit.
func TestAnUnconfidentSiteFetchesNothing(t *testing.T) {
	t.Parallel()

	learned := core.NewPredictions()

	const site = "Earthfile:12 command -v unbuffer"

	learned.Observe(site, true)
	learned.Needed(site, true, []string{testBaseImage})

	p := &puller{}

	prefetch(context.Background(), learned, p.pull)()

	if got := p.got(); got != "" {
		t.Errorf("fetched %q on a single observation", got)
	}
}

// An alternating site fetches nothing either: half of every pull would be
// wasted, and the engine has better uses for the bandwidth.
func TestAnAlternatingSiteFetchesNothing(t *testing.T) {
	t.Parallel()

	learned := core.NewPredictions()

	const site = "Earthfile:12 test -f flag"

	for i := range 8 {
		learned.Observe(site, i%2 == 0)
	}

	learned.Needed(site, true, []string{testBaseImage})
	learned.Needed(site, false, []string{"debian:12"})

	p := &puller{}

	prefetch(context.Background(), learned, p.pull)()

	if got := p.got(); got != "" {
		t.Errorf("fetched %q for a condition that alternates", got)
	}
}

// A pull that fails is not a build failure.
//
// Prefetching is a hint (I5): the image will be pulled again, properly, when
// something actually needs it. A prefetch that could fail a build would make a
// hint load-bearing.
func TestAFailedPrefetchIsNotAFailure(t *testing.T) {
	t.Parallel()

	learned := core.NewPredictions()

	const site = "Earthfile:12 command -v unbuffer"

	for range 4 {
		learned.Observe(site, true)
	}

	learned.Needed(site, true, []string{testBaseImage})

	prefetch(context.Background(), learned, func(context.Context, string) error {
		return context.DeadlineExceeded
	})()
}

// After a build, each condition it evaluated records what the build needed.
//
// Over-inclusive on purpose: what is recorded is every image the plan used, not
// a precise subtree. Attributing images to a branch exactly would need
// bookkeeping the interpreter has no other reason to carry, and being wrong in
// this direction costs bandwidth - which is the whole reason this tier is free.
func TestABuildRecordsWhatItsConditionsLedTo(t *testing.T) {
	t.Parallel()

	learned := core.NewPredictions()

	recordNeeds(learned, map[string]bool{
		"Earthfile:12 command -v unbuffer": true,
		"Earthfile:40 test -f flag":        false,
	}, []string{testBaseImage, "golang:1.26", testBaseImage})

	// Deduplicated and sorted, so the record does not depend on graph order.
	if got := strings.Join(learned.Needs("Earthfile:12 command -v unbuffer", true), ","); got != testTwoImages {
		t.Errorf("recorded %q", got)
	}

	// Against the branch that was actually taken, not the other one.
	if got := learned.Needs("Earthfile:12 command -v unbuffer", false); len(got) != 0 {
		t.Errorf("the untaken branch recorded %v", got)
	}

	if got := strings.Join(learned.Needs("Earthfile:40 test -f flag", false), ","); got != testTwoImages {
		t.Errorf("the second site recorded %q", got)
	}
}

// A build that evaluated no conditions records nothing, rather than attributing
// its images to a site that does not exist.
func TestABuildWithNoConditionsRecordsNothing(t *testing.T) {
	t.Parallel()

	learned := core.NewPredictions()

	recordNeeds(learned, nil, []string{testBaseImage})

	if len(learned.Sites()) != 0 {
		t.Errorf("a build with no conditions left %v behind", learned.Sites())
	}
}

// An image declared for publishing says so, and says it was not published.
//
// `SAVE IMAGE --push` is a declaration that the invocation decides on, and this
// engine has no flag to decide it with - so not pushing is correct. Saying
// nothing is not: someone who wrote `--push` and watched a build succeed has
// been given every reason to believe it was published.
func TestAnImageDeclaredForPushSaysItWasNotPushed(t *testing.T) {
	t.Parallel()

	if got := pushNote(true); got == "" {
		t.Error("a pushable image is written with no mention of the push")
	} else if !strings.Contains(got, "not pushed") {
		t.Errorf("the note does not say what did not happen: %q", got)
	}

	if got := pushNote(false); got != "" {
		t.Errorf("an ordinary image carries a note about pushing: %q", got)
	}
}

// A prefetch runs beside the build, not in front of it.
//
// This is the whole claim the tier makes: an image pull is network-bound and
// nothing else in a build proceeds during it, so the transfer belongs off the
// critical path. Waiting for the pulls before interpreting anything puts them
// at the *head* of that path instead, serialised - which is strictly worse than
// not prefetching at all, because a wrong prediction is then paid for in full
// before the build has started.
func TestAPrefetchDoesNotBlockTheBuild(t *testing.T) {
	t.Parallel()

	learned := core.NewPredictions()
	site := "Earthfile:12 command -v unbuffer"

	for range 4 {
		learned.Observe(site, true)
	}

	learned.Needed(site, true, []string{testBaseImage})

	started := make(chan struct{})
	release := make(chan struct{})

	wait := prefetch(context.Background(), learned, func(context.Context, string) error {
		close(started)
		<-release

		return nil
	})

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the prefetch never started")
	}

	// The point: control is back here while the pull is still in flight.
	done := make(chan struct{})

	go func() {
		wait()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("the prefetch finished before its pull did, so it was not really running")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waiting for the prefetch never returned")
	}
}

// And the waiter is not optional bookkeeping: it exists so a build that has
// finished does not leave pulls running against a cache directory it is about
// to stop using.
func TestAPrefetchIsFinishedBeforeTheBuildReturns(t *testing.T) {
	t.Parallel()

	learned := core.NewPredictions()
	site := "Earthfile:1 test"

	for range 4 {
		learned.Observe(site, true)
	}

	learned.Needed(site, true, []string{"a:1", "b:1", "c:1"})

	var (
		mu   sync.Mutex
		done int
	)

	wait := prefetch(context.Background(), learned, func(context.Context, string) error {
		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		done++
		mu.Unlock()

		return nil
	})

	wait()

	mu.Lock()
	defer mu.Unlock()

	if done != 3 {
		t.Errorf("%d of 3 pulls had finished when the waiter returned", done)
	}
}
