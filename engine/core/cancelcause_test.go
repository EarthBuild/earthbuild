package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A cancelled step says why it was cancelled, and names the root cause.
//
// `context canceled` is the news that this step stopped, which the author can
// see for themselves. What they cannot see is *what went wrong*, and that is the
// only actionable half - it is the thing buildkit does not tell you, and the
// reason a cancelled build there sends you reading logs to find the one step
// that actually failed.
func TestACancelledStepNamesTheRootCause(t *testing.T) {
	t.Parallel()

	root := &StepError{Source: "Earthfile:9", Desc: "RUN make", Exit: 1}

	got := cancelled("Earthfile:20", root)

	if !strings.Contains(got.Error(), "Earthfile:9") {
		t.Errorf("a cancelled step does not name what caused it: %v", got)
	}

	if !strings.Contains(got.Error(), "Earthfile:20") {
		t.Errorf("a cancelled step does not say which step it was: %v", got)
	}

	// The root is reachable, not just quoted, so a caller can ask what kind of
	// failure it was rather than reading prose.
	var step *StepError
	if !errors.As(got, &step) || step.Source != "Earthfile:9" {
		t.Errorf("the root failure is not reachable through the cancellation: %v", got)
	}
}

// A good cancel is not a failure.
//
// Two machines given the same step is the design working: one wins, the other is
// stopped, and nothing went wrong. Reporting it as a failure - or letting it be
// the thing a build is blamed on when everything else was cancelled - would make
// a successful optimisation look like a fault.
func TestASupersededStepIsNotAFailure(t *testing.T) {
	t.Parallel()

	good := cancelled("Earthfile:5", ErrSuperseded)

	if !benignCancel(good) {
		t.Error("a superseded step reads as a failure")
	}

	// And a cancellation caused by a real failure does not.
	bad := cancelled("Earthfile:5", &StepError{Source: "Earthfile:9", Exit: 1})
	if benignCancel(bad) {
		t.Error("a cancellation caused by a failure reads as benign")
	}

	// A bare context cancellation is not benign either: nothing said it was
	// good, and assuming so is how a real fault becomes silence.
	if benignCancel(context.Canceled) {
		t.Error("a bare cancellation with no cause reads as benign")
	}
}

// Superseded work is dropped from the report even when it is all there is.
//
// Every other cancellation survives that case, because a build that failed must
// say something. A superseded step is different in kind: it did not fail, it was
// not needed, and a build made entirely of them did not go wrong.
func TestSupersededWorkIsNeverReported(t *testing.T) {
	t.Parallel()

	got := independentFailures([]failed{
		{err: cancelled("Earthfile:5", ErrSuperseded), at: 0, key: "a"},
		{err: cancelled("Earthfile:7", ErrSuperseded), at: 1, key: "b"},
	}, func(string) (string, bool) { return "", false })

	if len(got) != 0 {
		t.Errorf("reported %d superseded steps as failures, want none", len(got))
	}
}
