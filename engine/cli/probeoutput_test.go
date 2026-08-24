package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A probe that failed reports what it printed.
//
// `substitute` and the condition evaluator both end their diagnostics with the
// command's output, because a command that failed is usually the one whose
// message matters most. They were ending them with nothing.
//
// Two things are true at once and only together do they lose it: `runGraph`
// captures the probe's own lines, and the *scheduler's* StepError carries no
// output for a step that streamed - which every step does when a build has a
// progress display. The failure path read the second and dropped the first, so
// a build that could not evaluate an ENV said only
//
//	ENV at Earthfile:862: "..." exited 128
//
// with an empty line where the reason should be. That is a real Earthfile in
// this repository, and it is what a native CI job failed on with nothing to go
// on.
func TestAFailedProbeCarriesWhatItPrinted(t *testing.T) {
	t.Parallel()

	const printed = "yq: command not found"

	run := func(context.Context, *ir.Graph) (string, error) {
		// What the scheduler gives back for a step that ran, failed, and had
		// its output streamed rather than returned.
		return printed, &core.StepError{Exit: 128, Streamed: true}
	}

	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"alpine"}}}

	res, err := decideByRunning(context.Background(), run, []string{"cat", "x"}, base, "/", "Earthfile:1")
	if err != nil {
		t.Fatalf("a step that ran and failed is a result, not an error: %v", err)
	}

	if res.Exit != 128 {
		t.Errorf("the exit status is %d, not the one the step gave", res.Exit)
	}

	if !strings.Contains(res.Output, printed) {
		t.Errorf("the result carries %q; the reason the command failed is gone", res.Output)
	}
}

// And where the scheduler does carry output, that is used.
//
// A step that did not stream has its output on the error, and it is the more
// direct source - the capture is a display-side copy of the same lines.
func TestAFailedProbePrefersTheOutputTheSchedulerCarried(t *testing.T) {
	t.Parallel()

	run := func(context.Context, *ir.Graph) (string, error) {
		return "", &core.StepError{Exit: 2, Output: "from the scheduler"}
	}

	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"alpine"}}}

	res, err := decideByRunning(context.Background(), run, []string{"false"}, base, "/", "Earthfile:1")
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(res.Output, "from the scheduler") {
		t.Errorf("the result carries %q", res.Output)
	}
}

// A probe whose *base* failed says which step failed, not the probe's own.
//
// Running a condition or a substitution means running the steps it stands on,
// and any of those can fail. The exit status comes back on the StepError - and
// so do the step's source line and its command, which were dropped. What a
// caller then read was
//
//	ENV at Earthfile:862: "export tmp=$(cat ...); ..." exited 128
//
// naming a command that had not run: the build failed fifteen seconds in, with
// no step or cache output at all, on a base that takes minutes to build. The
// number 128 belonged to something else entirely, and the message pointed at
// the wrong line of the wrong file.
func TestAProbeSaysWhichStepFailed(t *testing.T) {
	t.Parallel()

	run := func(context.Context, *ir.Graph) (string, error) {
		return "", &core.StepError{
			Source: "buildkitd/Earthfile:14",
			Desc:   "RUN git describe --tags",
			Exit:   128,
		}
	}

	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"alpine"}}}

	res, err := decideByRunning(context.Background(), run, []string{"cat", "x"}, base, "/", "Earthfile:862")
	if err != nil {
		t.Fatalf("a step that ran and failed is a result: %v", err)
	}

	for _, want := range []string{"buildkitd/Earthfile:14", "git describe"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("the result says %q, which does not name the step that"+
				" failed (%q)", res.Output, want)
		}
	}
}
