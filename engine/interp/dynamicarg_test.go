package interp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A `$(...)` in a build argument is run, or the build says it cannot run it.
//
// `tests/build-arg-dynamic-with-empty-base.earth`:
//
//	test:
//	    FROM busybox:1.38
//	    BUILD +subtest --myvar="$(busybox | head -1)"
//
// The value comes from running a command in *this* target's image, and the
// target it is passed to asserts what that command prints. This engine passed
// the empty string, so the assertion compared against nothing and the failure
// named the grep rather than the argument (E445).
//
// `ARG v = $(...)` already runs a probe; the same expression in a build argument
// did not, which is one expression with two meanings depending on where it is
// written.
func TestADynamicBuildArgumentIsRunOrRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\ndep:\n    FROM alpine:3.22\n    ARG v=none\n    RUN echo $v\n"+
		"\nmain:\n    FROM alpine:3.22\n    BUILD +dep --v=\"$(echo hello)\"\n",
		testMain)

	// No probe runner here, so the honest answers are two: run it (impossible)
	// or say it cannot be run. Passing the empty string is neither, and it is
	// the one that produces a wrong build reported as a success.
	if err == nil {
		t.Fatal("a dynamic build argument planned with no way to evaluate it" +
			"\n  the target it is passed to received an empty string")
	}

	if !errors.Is(err, interp.ErrNoRunner) && !errors.Is(err, interp.ErrNotProvided) {
		t.Errorf("refused with %q, which is not a withheld-capability refusal", err)
	}

	if !strings.Contains(err.Error(), "$(") && !strings.Contains(err.Error(), "echo hello") {
		t.Errorf("refused with %q, which does not name the expression it could"+
			" not evaluate", err)
	}
}

// With a runner, it is run - and run against the right image.
//
// The gate has a runner, and the corpus target still received an empty string.
// The value is a command run in *this* target's image and its output is what the
// other target is given: `--myvar="$(busybox | head -1)"` is a busybox banner or
// it is nothing (E445).
func TestADynamicBuildArgumentIsEvaluated(t *testing.T) {
	t.Parallel()

	var (
		asked [][]string
		bases []string
	)

	run := func(cmd []string, base *ir.Node, _, _ string) (interp.Result, error) {
		asked = append(asked, cmd)

		if base != nil {
			bases = append(bases, strings.Join(base.Op.Args, " "))
		}

		return interp.Result{Output: "hello\n"}, nil
	}

	p, err := interp.Build(versioned+
		"\ndep:\n    FROM alpine:3.22\n    ARG v=none\n    RUN echo [$v]\n"+
		"\nmain:\n    FROM alpine:3.22\n    BUILD +dep --v=\"$(echo hello)\"\n",
		testMain, interp.WithCommands(run))
	if err != nil {
		t.Fatalf("planning with a runner: %v", err)
	}

	if len(asked) == 0 {
		t.Fatal("nothing was run: the `$(...)` was not evaluated at all")
	}

	// Against the image the target is on at that line, not the file's base
	// recipe. `tests/build-arg-dynamic-with-empty-base.earth` exists to make
	// that distinction: it has no base recipe at all - the comment in it says so
	// - and its target sets `FROM busybox:1.38` before the BUILD line, so a
	// probe run against the base recipe has no `/bin/sh` to run at all (E445).
	if len(bases) == 0 || !strings.Contains(bases[0], "alpine") {
		t.Errorf("the probe was run against %v, and the target is on alpine at"+
			" that line", bases)
	}

	// Trailing newline gone, as a shell substitution has it: `$(echo hello)` is
	// `hello`, and a value with a newline in it reaches a command line that has
	// no idea what to do with one.
	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "[hello]") {
		t.Errorf("the dependent target runs with %q; it was passed hello", got)
	}
}

// A `$(...)` default keeps the whole command, spaces and all.
//
// `ARG V=$(cat ./content)` is two tokens by the time the interpreter sees it,
// and the default was taken from the first: the probe was asked to run `$(cat`
// and produced nothing, so the argument arrived empty and the failure named the
// assertion three lines later (E449).
//
// `tests/build-arg.earth` has four of these, and the one that reads
// `ARG VAR1=$(ls)` works - one token, no space in it - which is what kept the
// shape hidden.
func TestADynamicDefaultKeepsItsWholeCommand(t *testing.T) {
	t.Parallel()

	var asked []string

	run := func(cmd []string, _ *ir.Node, _, _ string) (interp.Result, error) {
		asked = append(asked, strings.Join(cmd, " "))

		return interp.Result{Output: "hello\n"}, nil
	}

	got := commandOfFirstExecWith(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n"+
		"    ARG V=$(cat ./content)\n    RUN echo [$V]\n",
		interp.WithCommands(run))

	if len(asked) != 1 || asked[0] != "cat ./content" {
		t.Errorf("the probe was asked to run %q, and the Earthfile wrote"+
			" `cat ./content`", asked)
	}

	if !strings.HasSuffix(got, "[hello]") {
		t.Errorf("the step runs %q, and the argument is what the probe printed", got)
	}
}

// Quoting inside the command survives too.
//
// `$(ls -a | tr '\n' ' ')` reached the shell with its quotes rearranged, because
// the command was rebuilt by joining tokens with single spaces - and a value
// reassembled from tokens is not the value that was written.
func TestADynamicDefaultKeepsItsQuoting(t *testing.T) {
	t.Parallel()

	var asked []string

	run := func(cmd []string, _ *ir.Node, _, _ string) (interp.Result, error) {
		asked = append(asked, strings.Join(cmd, " "))

		return interp.Result{Output: "x\n"}, nil
	}

	_ = commandOfFirstExecWith(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n"+
		"    ARG V=$(printf 'a b' | tr ' ' '-')\n    RUN echo [$V]\n",
		interp.WithCommands(run))

	if len(asked) != 1 || asked[0] != `printf 'a b' | tr ' ' '-'` {
		t.Errorf("the probe was asked to run %q, and the quotes are part of the"+
			" command", asked)
	}
}

// commandOfFirstExecWith is commandOfFirstExec with options.
func commandOfFirstExecWith(t *testing.T, src string, opts ...interp.Option) string {
	t.Helper()

	p, err := interp.Build(src, "main", opts...)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec {
			return strings.Join(n.Op.Args, " ")
		}
	}

	return ""
}
