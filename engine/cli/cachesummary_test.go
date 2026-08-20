package cli

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// A build says how much of it it did not have to do.
//
// The scheduler has been counting hits, misses, L2 hits and Φ flattenings on
// every build since the counters were written, and nothing has ever read them.
// For a *caching* build engine that is close to the one number a user wants:
// "did it use the cache" is the first question asked of any build that took
// longer than expected, and the engine had the answer and kept it.
//
// Found by the port guard rather than by anybody noticing - which is the point
// of the port guard. An output nobody reads is indistinguishable, from inside
// the code that fills it in, from one that is read constantly.
func TestTheSummarySaysWhatTheCacheDid(t *testing.T) {
	t.Parallel()

	got := cacheSummary(core.Stats{Hits: 7, Misses: 2})

	for _, want := range []string{"7", "2", "hit", "miss"} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary does not mention %q: %q", want, got)
		}
	}

	// It sits directly under the per-step rows and reads as their total, so it
	// has to be in their columns. Hand-counted padding put it one character out
	// - visible immediately in a real build and in none of the assertions above,
	// which is what assertions about content rather than shape are worth here.
	if !strings.HasPrefix(got, stepRow("cache", "", "")[:16]) {
		t.Errorf("the summary is not in the step rows' columns:\n  %q\n  %q",
			got, stepRow("Earthfile:4", "L1 hit", "FROM alpine"))
	}
}

// A build with nothing to summarise says nothing.
//
// A dry run, a plan-only invocation, or a build whose every step is `LOCALLY` -
// host steps are never cached - all reach the end with an empty Stats. A line
// reading "0 hit, 0 miss" is worse than no line: it invites the reader to
// wonder which of the two numbers is the broken one.
func TestAnEmptyBuildSummarisesNothing(t *testing.T) {
	t.Parallel()

	if got := cacheSummary(core.Stats{}); got != "" {
		t.Errorf("a build that looked nothing up printed: %q", got)
	}
}

// The numbers that are only sometimes interesting appear only when they are.
//
// L2 hits and Φ flattenings are both zero on nearly every build - L2 is inert
// until S5 lands, and a stack deep enough to need flattening is rare. Printing
// them always would put two permanent zeroes in front of the two numbers that
// vary, and a reader learns to skip the whole line.
func TestTheRareCountsAppearOnlyWhenTheyAreNotZero(t *testing.T) {
	t.Parallel()

	plain := cacheSummary(core.Stats{Hits: 1, Misses: 1})
	if strings.Contains(plain, "flatten") || strings.Contains(plain, "observed") {
		t.Errorf("an ordinary build was told about counts that were zero: %q", plain)
	}

	rich := cacheSummary(core.Stats{Hits: 1, Misses: 1, L2Hits: 3, Flattened: 2})

	for _, want := range []string{"3", "2", "observed", "flatten"} {
		if !strings.Contains(rich, want) {
			t.Errorf("the summary does not mention %q: %q", want, rich)
		}
	}
}

// A stale prediction is reported as a rate, not as a raw count.
//
// `L2Stale` on its own says nothing: four stale predictions out of four is a
// profile store that has stopped working, and four out of four hundred is
// normal. The count without its denominator is the shape of statistic that gets
// quoted in a bug report and cannot be acted on - which this branch has already
// done once, with a lint total that was a number and its echo (E73).
func TestStalePredictionsAreReportedAgainstTheirTotal(t *testing.T) {
	t.Parallel()

	got := cacheSummary(core.Stats{Hits: 1, Misses: 3, L2Hits: 1, L2Stale: 2})

	if !strings.Contains(got, "3") {
		t.Errorf("the stale count is not reported against the attempts: %q", got)
	}

	if !strings.Contains(got, "stale") {
		t.Errorf("the summary does not mention stale predictions: %q", got)
	}
}
