package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// The build says what it spent, in the words the corpus looks for.
//
// `tests/Earthfile` drives `stats.earth` with
// `--output_contains="total CPU:.*total memory:.*"` - the shape a reader of the
// other engine's output already knows, which is the reason to match it rather
// than invent a better one (E467).
func TestTheUsageSummarySaysWhatTheBuildSpent(t *testing.T) {
	t.Parallel()

	got := usageSummary(core.Stats{CPU: 3*time.Second + 500*time.Millisecond, MaxRSS: 2 << 30})

	for _, want := range []string{"total CPU:", "total memory:", "3.5s", "2.1 GB"} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary is %q, which does not contain %q", got, want)
		}
	}
}

// A build that measured nothing says nothing rather than something wrong.
//
// Zero CPU and zero memory is what a build of cache hits spends, and what a
// backend that cannot measure reports. Printing `0s` and `0 B` is honest; the
// failure to avoid is a plausible number nobody produced.
func TestABuildThatSpentNothingSaysSo(t *testing.T) {
	t.Parallel()

	got := usageSummary(core.Stats{})

	if !strings.Contains(got, "0s") || !strings.Contains(got, "0 B") {
		t.Errorf("the summary is %q, and this build spent nothing", got)
	}
}
