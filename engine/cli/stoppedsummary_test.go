package cli

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A failed build says which steps it stopped, and what stopped them.
//
// The per-step table is printed after the build returns its error, so a *failed*
// build printed no step lines at all - and the cancellation records the
// scheduler now keeps were invisible to the one person who needs them. The
// author saw the root failure and had no way to tell which of their parallel
// steps had been abandoned partway (E969).
func TestAFailedBuildSaysWhatItStopped(t *testing.T) {
	t.Parallel()

	got := stoppedSummary([]core.StepRecord{
		{Outcome: core.OutcomeMiss, Meta: ir.Meta{Source: "Earthfile:9", Description: "RUN make"}},
		{
			Outcome: core.OutcomeCancelled, Cause: "Earthfile:9 failed",
			Meta: ir.Meta{Source: "Earthfile:20", Description: "RUN sleep 30"},
		},
		{
			Outcome: core.OutcomeCancelled, Cause: "Earthfile:9 failed",
			Meta: ir.Meta{Source: "Earthfile:24", Description: "RUN sleep 40"},
		},
	})

	for _, want := range []string{"Earthfile:20", "Earthfile:24", "cancelled", "Earthfile:9 failed"} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary does not mention %q:\n%s", want, got)
		}
	}

	// The step that actually failed is not listed as stopped: it is the cause,
	// and the error above already names it.
	if strings.Contains(got, "RUN make") {
		t.Errorf("the failing step was listed among the ones it stopped:\n%s", got)
	}
}

// Nothing stopped, nothing said.
//
// A build that failed on its own with nothing running beside it should not gain
// an empty section - "0 steps stopped" invites the reader to wonder which of the
// zeroes is broken, exactly as "0 hit, 0 miss" does.
func TestABuildThatStoppedNothingSaysNothing(t *testing.T) {
	t.Parallel()

	if got := stoppedSummary(nil); got != "" {
		t.Errorf("a build that stopped nothing said %q", got)
	}

	only := []core.StepRecord{{Outcome: core.OutcomeMiss, Meta: ir.Meta{Source: "Earthfile:9"}}}
	if got := stoppedSummary(only); got != "" {
		t.Errorf("a build with no cancellations said %q", got)
	}
}

// A wide fan is summarised rather than listed line by line.
//
// Twenty parallel steps stopped by one failure is twenty lines of the same news
// pushing the actual error off the top of the terminal. The first few name
// themselves and the rest are counted.
func TestManyStoppedStepsAreCounted(t *testing.T) {
	t.Parallel()

	steps := make([]core.StepRecord, 0, 12)
	for i := range 12 {
		steps = append(steps, core.StepRecord{
			Outcome: core.OutcomeCancelled, Cause: "Earthfile:9 failed",
			Meta: ir.Meta{Source: "Earthfile:" + string(rune('a'+i)), Description: "RUN x"},
		})
	}

	got := stoppedSummary(steps)
	if lines := strings.Count(got, "\n"); lines > 7 {
		t.Errorf("twelve stopped steps produced %d lines:\n%s", lines, got)
	}

	if !strings.Contains(got, "12") {
		t.Errorf("the summary does not say how many were stopped:\n%s", got)
	}
}
