package core_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// A failure does not repeat output the user has already watched.
//
// The output is carried in the error because an exit code alone sends the
// reader back to run the command by hand. But when the build was *streaming* -
// which it is whenever anybody is watching - the same lines have already gone
// past, so printing them again produces the command's output twice, the second
// copy truncated at the guest's cap.
//
// That is not merely untidy. It sent me down a two-hour diagnosis: a log with
// the output in it twice made a `grep -c` over `+lint` count most findings
// twice, and the resulting total moved for reasons that had nothing to do with
// the code (E73). The engine had been reporting a number and its echo.
func TestAStreamedFailureDoesNotRepeatItself(t *testing.T) {
	t.Parallel()

	err := &core.StepError{
		Source: at(9), Desc: testRunMake, Exit: 2,
		Output:   "undefined reference to `main'\ncollect2: error: ld returned 1",
		Streamed: true,
	}

	got := err.Error()

	if strings.Contains(got, "undefined reference") {
		t.Errorf("the error repeated output the user already saw:\n%s", got)
	}

	// It still has to say where and what: dropping the output must not drop the
	// attribution with it.
	for _, want := range []string{testRunMake, "exit code 2", at(9)} {
		if !strings.Contains(got, want) {
			t.Errorf("the error no longer mentions %q:\n%s", want, got)
		}
	}

	// And it must say the output is elsewhere, or a reader who scrolled past it
	// is told nothing about where it went.
	if !strings.Contains(got, "above") {
		t.Errorf("the error does not say where the output is:\n%s", got)
	}
}

// When nothing was streamed, the output is the whole point.
//
// A caller with no progress sink - a test, a machine-readable front end, a
// build whose output nobody watched - has seen nothing, so the error carries it
// exactly as before. This is the arm that stops the fix above from turning a
// diagnosable failure into an exit code.
func TestAnUnstreamedFailureStillCarriesItsOutput(t *testing.T) {
	t.Parallel()

	err := &core.StepError{
		Source: at(9), Desc: testRunMake, Exit: 2,
		Output: "undefined reference to `main'",
	}

	if got := err.Error(); !strings.Contains(got, "undefined reference") {
		t.Errorf("an unwatched failure lost its output:\n%s", got)
	}
}
