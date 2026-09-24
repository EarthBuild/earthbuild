package cli

import (
	"fmt"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// listedStopped is how many stopped steps are named before the rest are counted.
//
// Twenty parallel steps stopped by one failure is twenty lines of identical news
// pushing the actual error off the top of the terminal. A few name themselves,
// so the author can see *which* work was abandoned, and the tail is a number.
const listedStopped = 5

// stoppedSummary says which steps a failure stopped, and what stopped them.
//
// **The per-step table never reaches a failed build.** It is printed after the
// build's error is returned, so a build that failed printed no step lines at
// all: the author was told what broke and nothing about the work abandoned
// beside it. On a wide fan that is most of the build (E969).
//
// Empty when nothing was stopped, for the reason cacheSummary is empty when
// nothing was looked up: "0 steps stopped" invites a reader to wonder which of
// the zeroes is broken.
//
// The failing step is not among these. It is the cause, and the error printed
// above already names it - listing it here as something it stopped would read as
// though it had stopped itself.
func stoppedSummary(steps []core.StepRecord) string {
	stopped := make([]core.StepRecord, 0, len(steps))

	for _, r := range steps {
		if r.Outcome == core.OutcomeCancelled {
			stopped = append(stopped, r)
		}
	}

	if len(stopped) == 0 {
		return ""
	}

	var b strings.Builder

	for i, r := range stopped {
		if i == listedStopped {
			break
		}

		b.WriteString(stepRow(r.Meta.Source, r.Outcome.String(), r.Meta.Description))
	}

	// One line for the whole set, naming the cause once. Repeating it on every
	// row would put the same sentence down the screen and bury the sources,
	// which are the part that differs.
	b.WriteString(stepRow("stopped", "", stoppedLine(stopped)))

	return b.String()
}

// stoppedLine is the sentence under the rows: how many, and why.
//
// Causes are named individually while there are few of them, because two
// different reasons in one build is a fact worth seeing; past that they are
// counted, since a list of twenty is not read.
func stoppedLine(stopped []core.StepRecord) string {
	causes := make([]string, 0, len(stopped))
	seen := map[string]bool{}

	for _, r := range stopped {
		if r.Cause == "" || seen[r.Cause] {
			continue
		}

		seen[r.Cause] = true
		causes = append(causes, r.Cause)
	}

	what := fmt.Sprintf("%d stopped", len(stopped))
	if len(stopped) > listedStopped {
		what = fmt.Sprintf("%d stopped, %d not listed", len(stopped), len(stopped)-listedStopped)
	}

	switch len(causes) {
	case 0:
		return what
	case 1:
		return what + ", by " + causes[0]
	default:
		return fmt.Sprintf("%s, by %d causes: %s", what, len(causes), strings.Join(causes, "; "))
	}
}
