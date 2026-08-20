package interp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// recorder is a stand-in for running a condition in a sandbox.
//
// The point of a seam here is that the *decision* about which conditions need
// evaluating, and what happens to the answer, is testable without a VM, an
// image or a network - which is what makes it testable at all on every machine
// and every change.
type recorder struct {
	calls  [][]string
	bases  []*ir.Node
	result bool
	output string
	err    error
}

func (r *recorder) run(cmd []string, base *ir.Node, _, _ string) (interp.Result, error) {
	r.calls = append(r.calls, cmd)
	r.bases = append(r.bases, base)

	exit := 1
	if r.result {
		exit = 0
	}

	return interp.Result{Exit: exit, Output: r.output}, r.err
}

const condSrc = `
main:
    FROM alpine:3.22
    RUN prepare
    IF command -v unbuffer
        RUN with-unbuffer
    ELSE
        RUN without-unbuffer
    END
`

// A condition the interpreter cannot decide is evaluated, and its answer picks
// the branch.
func TestAnUndecidableConditionIsEvaluated(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		result bool
		want   string
		absent string
	}{
		{result: true, want: "with-unbuffer", absent: "without-unbuffer"},
		{result: false, want: "without-unbuffer", absent: "with-unbuffer"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()

			r := &recorder{result: tc.result}

			p, err := interp.Build(versioned+condSrc, testMain, interp.WithCommands(r.run))
			if err != nil {
				t.Fatal(err)
			}

			got := describe(p.Graph.Nodes())

			if !strings.Contains(got, tc.want) {
				t.Errorf("the %s branch is not in the graph:\n%s", tc.want, got)
			}

			if strings.Contains(got, tc.absent) {
				t.Errorf("the untaken branch %s is in the graph:\n%s", tc.absent, got)
			}

			if len(r.calls) != 1 {
				t.Fatalf("the condition was evaluated %d times, want 1", len(r.calls))
			}

			if joined := strings.Join(r.calls[0], " "); joined != testUnbufferProbe {
				t.Errorf("evaluated %q, want the condition as written", joined)
			}
		})
	}
}

// The evaluator is given the filesystem the condition is written against.
//
// `IF command -v unbuffer` asks about the image the recipe has built up to that
// line, not about a bare image and not about the host. Handing over the wrong
// base would answer a different question and be indistinguishable from a
// correct answer.
func TestAConditionIsEvaluatedAgainstThePrecedingStep(t *testing.T) {
	t.Parallel()

	r := &recorder{}

	_, err := interp.Build(versioned+condSrc, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	if len(r.bases) != 1 || r.bases[0] == nil {
		t.Fatal("the evaluator was given no base to run against")
	}

	if got := r.bases[0].Meta.Description; !strings.Contains(got, "prepare") {
		t.Errorf("the condition runs on %q, want the step just before it", got)
	}
}

// A condition that can be decided from the build arguments is never evaluated.
//
// Evaluating it would be correct and ruinous: it spends a sandbox on a string
// comparison, at every IF in every Earthfile, for an answer already in hand.
func TestDecidableConditionsAreNotEvaluated(t *testing.T) {
	t.Parallel()

	r := &recorder{result: true}

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG mode=debug
    IF [ "$mode" = "release" ]
        RUN release-only
    ELSE
        RUN debug-only
    END
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	if len(r.calls) != 0 {
		t.Errorf("a decidable condition was sent to the evaluator: %v", r.calls)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "debug-only") {
		t.Errorf("the decidable condition took the wrong branch:\n%s", got)
	}
}

// An evaluator that fails reports the condition and where it is, rather than
// the bare error from whatever ran it.
func TestAFailingEvaluatorNamesTheCondition(t *testing.T) {
	t.Parallel()

	r := &recorder{err: errors.New("the sandbox went away")}

	_, err := interp.Build(versioned+condSrc, testMain, interp.WithCommands(r.run))
	if err == nil {
		t.Fatal("a condition that could not be evaluated was accepted")
	}

	for _, want := range []string{testUnbufferProbe, "Earthfile:", "the sandbox went away"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%s", want, err)
		}
	}
}

// With no evaluator the condition is still refused, and still says so.
//
// This is the plan-only path - `earthbuild plan`, the corpus, any caller that
// wants a graph without running anything - and it must not start a sandbox
// behind the caller's back to produce one.
func TestWithoutAnEvaluatorAConditionIsStillRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+condSrc, testMain)
	if err == nil {
		t.Fatal("an undecidable condition was accepted with no evaluator")
	}

	if !strings.Contains(err.Error(), "needs to run") {
		t.Errorf("the refusal no longer says what it needs:\n%s", err)
	}
}

// `LET x = $(cmd)` takes the command's output as the value.
//
// The trailing newline goes: `LET tag = $(cat version)` means the version, not
// the version with a newline that then appears in an image tag.
func TestLetTakesCommandOutput(t *testing.T) {
	t.Parallel()

	r := &recorder{result: true, output: "v1.2.3\n"}

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    LET tag = $(cat version)
    RUN tag-is-$tag
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "tag-is-v1.2.3") {
		t.Errorf("the value did not reach the command:\n%s", got)
	}
}

// Without a runner, a computed value is refused by name.
func TestLetWithoutARunnerIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    LET tag = $(cat version)\n", testMain)
	if err == nil {
		t.Fatal("a computed value was accepted with no way to compute it")
	}

	if !strings.Contains(err.Error(), "cat version") {
		t.Errorf("the refusal does not name the command:\n%s", err)
	}
}

// A `$(...)` in a value the engine consumes is run, not carried through.
//
// `SAVE IMAGE app:$(cat version)` was producing an image reference containing
// the text `$(cat version)` - which is not a reference, and would have been
// pushed under that name or refused by the registry much later. The corpus has
// seven of them.
//
// The distinction that matters is which values this applies to. A RUN command
// is handed to a shell, and its `$(...)` is the shell's to expand; a value the
// *engine* reads - an image name, a path, a working directory - has no shell to
// expand it, so the engine must.
func TestValuesTheEngineConsumesHaveTheirCommandsRun(t *testing.T) {
	t.Parallel()

	r := &recorder{result: true, output: "1.2.3\n"}

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN build
    SAVE IMAGE app:$(cat version)
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Images) != 1 || p.Images[0].Ref != "app:1.2.3" {
		t.Fatalf("the image is %+v, want app:1.2.3", p.Images)
	}
}

// A RUN command's own `$(...)` is left for the shell.
//
// Running it here would evaluate it once, at plan time, and bake the answer
// into the command - so a step that reads the clock or lists a directory it is
// about to change would see the wrong moment.
func TestARunCommandKeepsItsOwnSubstitutions(t *testing.T) {
	t.Parallel()

	r := &recorder{result: true, output: "should-not-be-used\n"}

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN echo $(date)\n", testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "$(date)") {
		t.Errorf("the shell's substitution was evaluated at plan time:\n%s", got)
	}

	if len(r.calls) != 0 {
		t.Errorf("a RUN command's substitution was run by the engine: %v", r.calls)
	}
}

// Without a runner it is refused, naming the command.
func TestAValueNeedingACommandIsRefusedWithoutARunner(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN build\n    SAVE IMAGE app:$(cat version)\n", testMain)
	if err == nil {
		t.Fatal("an image reference needing a command was accepted")
	}

	if !strings.Contains(err.Error(), "cat version") {
		t.Errorf("the refusal does not name the command:\n%s", err)
	}
}
