package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

const withFunction = versioned + `
main:
    FROM alpine:3.22
    DO +GREET --name=world
    RUN after

GREET:
    FUNCTION
    ARG name
    RUN echo hello $name
`

// A function's commands are inlined into the caller's chain.
//
// Unlike BUILD, which runs another target beside this one, DO continues *this*
// target's filesystem. That is the distinction: a function is a way of writing
// the same steps in one place, not a way of running a different build.
func TestDoInlinesTheFunction(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(withFunction, testMain)
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())

	for _, want := range []string{"echo hello world", testAfter} {
		if !strings.Contains(got, want) {
			t.Errorf("the graph is missing %q:\n%s", want, got)
		}
	}

	// The step after the DO stands on the function's output, not beside it.
	nodes := p.Graph.Nodes()
	last := nodes[len(nodes)-1]

	if !strings.Contains(last.Meta.Description, "after") {
		t.Fatalf("the last step is %q", last.Meta.Description)
	}

	if len(last.Inputs) == 0 || !strings.Contains(last.Inputs[0].Meta.Description, testGreeting) {
		t.Error("the step after DO does not continue from the function's last step")
	}
}

// Arguments are passed as --name=value and are in scope inside the function.
func TestDoPassesArguments(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(withFunction, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "hello world") {
		t.Errorf("the argument did not reach the function:\n%s", got)
	}
}

// A function's own ARG default applies when the caller passes nothing.
func TestFunctionDefaultsApply(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    DO +GREET

GREET:
    FUNCTION
    ARG name=default
    RUN echo hello $name
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "hello default") {
		t.Errorf("the function's default was not used:\n%s", got)
	}
}

// The same function called with different arguments is different steps.
//
// A function that produced one step however it was called would be a false hit
// with a new syntax.
func TestDifferentArgumentsProduceDifferentSteps(t *testing.T) {
	t.Parallel()

	src := versioned + `
main:
    FROM alpine:3.22
    DO +GREET --name=one
    DO +GREET --name=two

GREET:
    FUNCTION
    ARG name
    RUN echo hello $name
`

	p, err := interp.Build(src, testMain)
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	for _, want := range []string{"hello one", "hello two"} {
		if !strings.Contains(got, want) {
			t.Errorf("the graph is missing %q:\n%s", want, got)
		}
	}
}

// The caller's arguments are not silently visible inside a function.
//
// A function is a unit with its own interface: values arrive through it, or the
// function quietly depends on where it was called from and moving the call
// changes what it does.
func TestCallerArgumentsDoNotLeakIn(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG secret=visible
    DO +SHOW

SHOW:
    FUNCTION
    RUN echo $secret
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); strings.Contains(got, "visible") {
		t.Errorf("the caller's argument leaked into the function:\n%s", got)
	}
}

// A function that does not exist lists what does.
func TestUnknownFunctionListsAlternatives(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine
    DO +GRET

GREET:
    FUNCTION
    RUN true
`, testMain)
	if err == nil {
		t.Fatal("a call to a missing function was accepted")
	}

	for _, want := range []string{"GRET", "GREET"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}

// A function that calls itself is refused, like a target cycle.
func TestRecursiveFunctionsAreRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine
    DO +LOOP

LOOP:
    FUNCTION
    DO +LOOP
`, testMain)
	if err == nil {
		t.Fatal("a recursive function was accepted")
	}

	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error does not name the cycle:\n%s", err)
	}
}

// DO with no base is refused: a function's commands need a filesystem.
func TestDoBeforeFromIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+"\nmain:\n    DO +X\n\nX:\n    FUNCTION\n    RUN true\n", testMain)
	if err == nil {
		t.Fatal("DO with no base image was accepted")
	}
}

// Arguments come in two shapes and both are real.
//
// `--name=value` and `--name value` both appear in this repository's own
// Earthfiles. Handling only the first refused a line that is perfectly ordinary,
// with a message telling the author to write what they had already written.
func TestDoAcceptsBothArgumentForms(t *testing.T) {
	t.Parallel()

	for _, form := range []string{"--name=world", "--name world"} {
		t.Run(form, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    DO +GREET `+form+`

GREET:
    FUNCTION
    ARG name
    RUN echo hello $name
`, testMain)
			if err != nil {
				t.Fatal(err)
			}

			if got := describe(p.Graph.Nodes()); !strings.Contains(got, "hello world") {
				t.Errorf("the argument did not reach the function:\n%s", got)
			}
		})
	}
}

// A flag with no value is a boolean argument, which is what the repository's
// own flag parser does with one - `--name` means `--name=true`. Refusing it
// here would refuse a line the rest of the tool accepts.
func TestDoTreatsAValuelessArgumentAsABoolean(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    DO +GREET --name

GREET:
    FUNCTION
    RUN true
`, testMain)
	if err != nil {
		t.Fatalf("a boolean argument was refused: %v", err)
	}
}
