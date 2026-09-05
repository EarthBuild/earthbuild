package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// A build whose steps ran unbounded says so, once, in its own output.
//
// The guest carries the reason back per step now (E123). This is the other end:
// what a person sees. It follows `warnCaseInsensitive` exactly - a build-level
// note, written where the build writes, silent when there is nothing to say.
//
// **Once**, not per step. Every step degrades for the same reason, and a
// warning repeated forty times is a warning nobody reads - which is the same
// outcome as not printing it, reached more expensively.
func TestAnUnboundedBuildSaysSo(t *testing.T) {
	t.Parallel()

	t.Run("silent when limits were applied", func(t *testing.T) {
		t.Parallel()

		var b bytes.Buffer

		warnUnbounded(&b, "")

		if b.Len() != 0 {
			t.Errorf("a build whose limits were applied printed a warning: %q", b.String())
		}
	})

	t.Run("names the cause and what it means", func(t *testing.T) {
		t.Parallel()

		var b bytes.Buffer

		warnUnbounded(&b, "cgroup v2 is not mounted")

		out := b.String()

		// The cause, because "limits not applied" leaves a reader to guess
		// between an unmounted cgroup filesystem, a delegated subtree they may
		// not write, and a platform that has none.
		if !strings.Contains(out, "cgroup v2 is not mounted") {
			t.Errorf("the warning does not carry the guest's reason: %q", out)
		}

		// And the consequence, because a reader who does not know what a
		// resource limit was for cannot judge whether to care: a step that
		// would have been stopped at a ceiling now takes the machine down
		// with it instead.
		if !strings.Contains(out, "memory") && !strings.Contains(out, "unbounded") {
			t.Errorf("the warning does not say what was lost: %q", out)
		}
	})

	t.Run("a nil writer is not a crash", func(t *testing.T) {
		t.Parallel()

		warnUnbounded(nil, "anything")
	})
}

// The warning is reachable from a build.
//
// The other half of the pair, and the one this session keeps finding missing: a
// value produced by one side and never consumed by the other. `warnUnbounded`
// on its own is a function nobody calls, which is exactly what
// `srv.Degraded()` was before this - correct, tested, and printed after the
// build to a stream nobody reads.
func TestTheBuildAsksWhetherItWasUnbounded(t *testing.T) {
	t.Parallel()

	found, err := nonTestFilesContaining(".", "warnUnbounded(")
	if err != nil {
		t.Fatal(err)
	}

	// Its definition, and at least one caller.
	if len(found) < 2 {
		t.Errorf("warnUnbounded is defined and not called from a build: %v"+
			"\n  a warning nobody invokes is the shape the guest's own"+
			"\n  shutdown message already had", found)
	}
}

// A stale prediction reaches the person whose cache is not hitting.
//
// The engine knows which path disagreed at the moment it refuses (E127), and
// the summary said only how many. This is the other end of that: the reason has
// to survive from `WhyStale` through the scheduler's stats to the line a person
// reads, and each of those joins is where this session has found values that
// were produced and never consumed.
func TestTheCacheSummarySaysWhyAPredictionWentStale(t *testing.T) {
	t.Parallel()

	got := cacheSummary(core.Stats{
		Misses: 3, L2Stale: 1,
		StaleWhy: "/ changed in the base",
	})

	if !strings.Contains(got, "1 of 3 predictions stale") {
		t.Errorf("the count is gone: %q", got)
	}

	if !strings.Contains(got, "/ changed in the base") {
		t.Errorf("the summary counts stale predictions without saying why: %q", got)
	}
}

// And says nothing extra when there is nothing to say.
func TestTheCacheSummaryIsQuietWithoutAReason(t *testing.T) {
	t.Parallel()

	got := cacheSummary(core.Stats{Misses: 3, L2Stale: 1})

	if strings.Contains(got, "()") {
		t.Errorf("an empty reason left empty brackets: %q", got)
	}
}
