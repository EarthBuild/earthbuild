package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `FOR x IN a b c` unrolls at plan time.
//
// Unrolled rather than represented, for the reason IF is decided rather than
// deferred (green paper §3.4a): the graph stays known before the build. A loop
// in the graph would be a graph whose shape depends on something that has not
// happened yet, and every key, schedule and diagnostic here rests on the shape
// being settled first.
func TestForUnrollsItsBody(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    FOR flavour IN vanilla chocolate
        RUN make-$flavour
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	for _, want := range []string{"make-vanilla", "make-chocolate"} {
		if !strings.Contains(got, want) {
			t.Errorf("the body did not run for %q:\n%s", want, got)
		}
	}
}

// The iterations run in the order written, each standing on the last.
//
// A loop body usually builds on itself - the second iteration expects the
// first's files - so the chain is the meaning, not an implementation detail.
func TestForIterationsChainInOrder(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    FOR n IN one two three
        RUN step-$n
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var got []string

	for n := p.Graph.Root; n != nil; {
		if strings.HasPrefix(n.Meta.Description, "RUN step-") {
			got = append([]string{strings.TrimPrefix(n.Meta.Description, "RUN ")}, got...)
		}

		if len(n.Inputs) == 0 {
			break
		}

		n = n.Inputs[0]
	}

	if strings.Join(got, ",") != "step-one,step-two,step-three" {
		t.Errorf("iterations ran as %v, want the order written", got)
	}
}

// The loop variable is scoped to the loop, and does not outlive it.
func TestTheLoopVariableDoesNotEscape(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG flavour=outer
    FOR flavour IN inner
        RUN inside-$flavour
    END
    RUN after-$flavour
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	if !strings.Contains(got, "inside-inner") {
		t.Errorf("the loop variable was not in scope inside the loop:\n%s", got)
	}

	if !strings.Contains(got, "after-outer") {
		t.Errorf("the loop variable outlived the loop:\n%s", got)
	}
}

// A list held in an argument is expanded and then split.
func TestForOverAnArgument(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG targets="alpha beta"
    FOR t IN $targets
        RUN build-$t
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	for _, want := range []string{"build-alpha", "build-beta"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not in the graph:\n%s", want, got)
		}
	}
}

// `--sep` chooses what separates the items.
func TestForWithASeparator(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG list="a,b,c"
    FOR --sep="," item IN $list
        RUN item-$item
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	for _, want := range []string{"item-a", "item-b", "item-c"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not in the graph:\n%s", want, got)
		}
	}
}

// An empty list runs the body no times, and is not an error.
func TestForOverNothingRunsNothing(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG list=""
    FOR t IN $list
        RUN should-not-appear
    END
    RUN after
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	if strings.Contains(got, "should-not-appear") {
		t.Errorf("the body ran for an empty list:\n%s", got)
	}

	if !strings.Contains(got, "after") {
		t.Errorf("the step after the loop is missing:\n%s", got)
	}
}

// A list that has to be computed is refused by name.
//
// `FOR m IN $(find . -name go.mod)` needs a command run in the build
// environment, which is the same problem as a condition that cannot be decided
// - and the same answer until the evaluator returns output as well as status.
func TestForOverACommandIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    FOR m IN $(find . -name go.mod)
        RUN build-$m
    END
`, testMain)
	if err == nil {
		t.Fatal("a list that must be computed was accepted")
	}

	for _, want := range []string{"FOR", "find"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

// `FOR x IN $(cmd)` loops over what the command printed.
//
// The list is discovered by running something, so the graph is not fully known
// in advance - the same position a condition that needs a sandbox puts it in
// (green paper §3.4a), and answered through the same seam.
func TestForOverCommandOutput(t *testing.T) {
	t.Parallel()

	r := &recorder{result: true, output: "alpha\nbeta\ngamma\n"}

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    FOR d IN $(ls dirs)
        RUN build-$d
    END
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	for _, want := range []string{"build-alpha", "build-beta", "build-gamma"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not in the graph:\n%s", want, got)
		}
	}

	if len(r.calls) != 1 || strings.Join(r.calls[0], " ") != "ls dirs" {
		t.Errorf("ran %v, want the command inside the $()", r.calls)
	}
}

// A command that fails does not become a list.
//
// Looping over an error message would build one absurd iteration per word and
// report success.
func TestForOverAFailingCommandIsAnError(t *testing.T) {
	t.Parallel()

	r := &recorder{output: "ls: no such directory\n"}

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    FOR d IN $(ls nope)
        RUN build-$d
    END
`, testMain, interp.WithCommands(r.run))
	if err == nil {
		t.Fatal("a failing command was looped over")
	}

	if !strings.Contains(err.Error(), "no such directory") {
		t.Errorf("the error does not carry what the command said:\n%s", err)
	}
}
