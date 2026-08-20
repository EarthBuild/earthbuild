package cli

import (
	"fmt"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/core"
	"time"

	"github.com/dustin/go-humanize"
)

// cacheSummary is one line saying how much of the build was not run.
//
// The scheduler counted these from the beginning and nothing read them, which
// for a *caching* build engine loses close to the one number a user wants:
// "did it use the cache" is the first question asked of any build that took
// longer than expected, and the engine had the answer.
//
// Hits and misses always; the rest only when non-zero. L2 hits and Φ
// flattenings are zero on nearly every build - L2 is inert until S5 lands, and
// a stack deep enough to need flattening is rare - so printing them always
// would put two permanent zeroes in front of the two numbers that vary, and a
// reader learns to skip the line.
//
// Empty when nothing was looked up: a plan-only run, or a build whose every
// step is `LOCALLY`, since host steps are never cached. "0 hit, 0 miss" is
// worse than silence, because it invites the reader to wonder which of the two
// is broken.
// stepRow is a line of the per-step table: a source location, an outcome, and
// what happened.
//
// One format string for both the rows and the total under them, because they
// have to line up and hand-counted padding does not. The first attempt put the
// cache line one character out - obvious in a real build, invisible to every
// assertion about what the line *says*.
//
// Ten wide for the outcome because "uncaptured" is ten: a column narrower than
// its widest value shunts the description out of line on exactly the steps
// whose outcome most needs reading.
func stepRow(source, outcome, desc string) string {
	return fmt.Sprintf("  %-14s %-10s %s\n", source, outcome, desc)
}

func cacheSummary(s core.Stats) string {
	if s.Hits == 0 && s.Misses == 0 {
		return ""
	}

	parts := []string{fmt.Sprintf("%d hit, %d miss", s.Hits, s.Misses)}

	if s.L2Hits > 0 {
		parts = append(parts, fmt.Sprintf("%d by observed inputs", s.L2Hits))
	}

	// Against its denominator, never alone. Two stale predictions out of two is
	// a profile store that has stopped working and two out of two hundred is
	// ordinary; the count without the attempts is the shape of number that gets
	// quoted in a bug report and cannot be acted on (E73).
	if s.L2Stale > 0 {
		// With the cause, because the count alone says the tier is being
		// invalidated and not by what - and finding out cost a measurement the
		// engine could have spared (E127).
		stale := fmt.Sprintf("%d of %d predictions stale", s.L2Stale, s.Misses)
		if s.StaleWhy != "" {
			stale += " (" + s.StaleWhy + ")"
		}

		parts = append(parts, stale)
	}

	// Steps that will not be reusable *next* time, which is a different thing
	// from a miss and is otherwise invisible: a build can be entirely green,
	// entirely correct, and quietly storing nothing for the tier to find. That
	// state persisted through four experiments here before anybody could see it.
	if s.Unobserved > 0 {
		un := fmt.Sprintf("%d not observed", s.Unobserved)

		switch {
		case s.UnobservedWhy != "" && s.UnobservedWhere != "":
			un += " (" + s.UnobservedWhere + ": " + s.UnobservedWhy + ")"

		case s.UnobservedWhy != "":
			un += " (" + s.UnobservedWhy + ")"
		}

		parts = append(parts, un)
	}

	// Why the tier declined, when it did. A miss with no explanation is what
	// three experiments in a row had to add instrumentation to see.
	for _, d := range []struct {
		n    int
		what string
	}{
		{s.L2Unpredicted, "unpredicted" + where(s.L2UnpredictedAt)},
		{s.L2Empty, "predicting nothing"},
		{s.L2Unstored, "predicted and not stored"},
	} {
		if d.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", d.n, d.what))
		}
	}

	// Named, because a step that rebuilds every build is a cost somebody is
	// paying and the build was not mentioning it (E228).
	if s.Uncacheable > 0 {
		parts = append(parts,
			fmt.Sprintf("%d not cacheable%s", s.Uncacheable, where(s.UncacheableAt)))
	}

	if s.Flattened > 0 {
		parts = append(parts, fmt.Sprintf("%d flattened", s.Flattened))
	}

	return stepRow("cache", "", strings.Join(parts, ", "))
}

// where renders a few source locations for a summary, or nothing.
func where(at []string) string {
	if len(at) == 0 {
		return ""
	}

	return " (" + strings.Join(at, ", ") + ")"
}

// usageSummary is what the build spent, for `--exec-stats`.
//
// The phrasing is the corpus's: `tests/Earthfile` drives `stats.earth` with
// `--output_contains="total CPU:.*total memory:.*"`, which is the shape a reader
// of the other engine's output already knows (E467).
//
// Printed only when asked. A build that reported its resource use every time
// would be adding a line to every log for the sake of the builds that wanted it.
func usageSummary(s core.Stats) string {
	return stepRow("stats", "", fmt.Sprintf(
		"total CPU: %s  total memory: %s",
		s.CPU.Round(time.Millisecond), humanize.Bytes(s.MaxRSS)))
}
