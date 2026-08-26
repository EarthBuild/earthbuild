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

// A probe that failed silently still says something.
//
// The worst case for a diagnostic is the one that produced nothing: a step that
// exited non-zero having written not a byte. The message then reads
//
//	ENV at Earthfile:862: "..." exited 128
//
// followed by an empty line, and a reader has a number and nowhere to go. It
// happened, in CI, and cost three passes of reading logs that could not have
// answered the question.
//
// So where there is no output, say what is known instead: which step, what it
// ran, and that it said nothing - because "the command printed nothing" is
// itself a fact worth having, and it rules out half of what a reader would
// otherwise go and check.
func TestASilentFailureStillSaysSomething(t *testing.T) {
	t.Parallel()

	run := func(context.Context, *ir.Graph) (string, error) {
		// The probe itself, so Source matches where: nothing to attribute
		// elsewhere, and nothing printed.
		return "", &core.StepError{Source: "Earthfile:862", Desc: "IF cat x", Exit: 128}
	}

	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"alpine"}}}

	res, err := decideByRunning(context.Background(), run, []string{"cat", "x"}, base, "/", "Earthfile:862")
	if err != nil {
		t.Fatal(err)
	}

	if strings.TrimSpace(res.Output) == "" {
		t.Error("a step that failed silently produced an empty explanation," +
			" which is a number and nowhere to go")
	}
}

// TestAProbeRunsEveryTimeBecauseItsOutputIsItsValue.
//
// **A probe answered the right thing once and empty forever after.** From a
// cold cache, `LET v=$(ls -d helloworld*)` gave the three files; the second run
// and every run after gave "". Nothing failed - the empty string was carried
// into the variable, `IF [ "$v" != "" ]` went false, and the target asserted
// its way to "found 0 files" with the files plainly in the image.
//
// The mechanism is that a probe's *output* is its result, and output is the one
// thing a cache hit does not reproduce. `runGraph` collects the probe's lines
// through the executor's `Capture` hook, which fires when a step runs; a step
// whose key is already known does not run, so nothing is captured and the
// caller reads an empty string as an answer.
//
// So the probe declares what is true of it: it is not a function of its inputs
// in the way a build step is - two identical `ls` invocations over the same
// layers are the same *build*, and only one of them tells us what it printed.
// `NoCache` is in the key, so this does not silently share an entry with a
// step of the same shape.
//
// The failure class is a cache that reproduces a step's *effects* but not its
// *observations*, and it is invisible by construction: the answer is a value,
// not an error, so every check downstream believes it.
func TestAProbeRunsEveryTimeBecauseItsOutputIsItsValue(t *testing.T) {
	t.Parallel()

	var got *ir.Graph

	run := func(_ context.Context, g *ir.Graph) (string, error) {
		got = g

		return "the answer", nil
	}

	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"alpine"}}}

	_, err := decideByRunning(context.Background(), run, []string{"ls"}, base, "/", "Earthfile:1")
	if err != nil {
		t.Fatal(err)
	}

	if got == nil || got.Root == nil {
		t.Fatal("no graph was run")
	}

	if !got.Root.Op.NoCache {
		t.Error("the probe may be answered from cache, and a cache hit does not" +
			" run the step - so its output, which is the whole of its value," +
			" comes back empty and is believed")
	}

	// The step it stands on is untouched: only the observation is uncacheable,
	// and marking the base too would rebuild the build to read one line.
	if base.Op.NoCache {
		t.Error("the probe made its base uncacheable, which rebuilds the steps" +
			" under it every time a variable is read")
	}
}
