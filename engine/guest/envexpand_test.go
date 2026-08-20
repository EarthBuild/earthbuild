package guest

import (
	"slices"
	"testing"
)

// An ENV value referring to another variable gets its value.
//
// `ENV MYPATH=hello:$PATH` means the base image's PATH, and the engine set the
// literal string `hello:$PATH` instead - so a step that put a directory on its
// PATH lost everything already on it, silently, and only failed when something
// it needed was no longer found (E422).
//
// Expanded here rather than in the interpreter because this is where the base
// image's environment is known: at plan time `$PATH` is whatever the image says,
// and the image is an input the plan does not read.
func TestAnEnvValueReferringToAnotherGetsItsValue(t *testing.T) {
	t.Parallel()

	got := stepEnv(
		[]string{"PATH=/usr/bin:/bin", "LANG=C"},
		[]string{"MYPATH=hello:$PATH", "BOTH=${LANG}-x"},
	)

	for _, want := range []string{"MYPATH=hello:/usr/bin:/bin", "BOTH=C-x"} {
		if !slices.Contains(got, want) {
			t.Errorf("no %q in the step's environment:\n  %v", want, got)
		}
	}
}

// A later ENV sees an earlier one.
//
// `ENV A=1` then `ENV B=$A/2` is the order the file is written in, and each line
// is state the next one stands on - the same rule every other per-step
// declaration follows here.
func TestALaterEnvSeesAnEarlierOne(t *testing.T) {
	t.Parallel()

	got := stepEnv(nil, []string{"A=1", "B=$A/2"})

	if !slices.Contains(got, "B=1/2") {
		t.Errorf("a later ENV did not see the earlier one:\n  %v", got)
	}
}

// A name nothing defines expands to nothing, as a shell does.
//
// Not left as the literal text: `$NOPE` reaching a step as four characters is
// how this bug read from the outside, and a build that meant the text can write
// `$$NOPE`.
func TestAnUndefinedNameExpandsToNothing(t *testing.T) {
	t.Parallel()

	got := stepEnv(nil, []string{"X=[$NOPE]", "Y=[$$KEPT]"})

	if !slices.Contains(got, "X=[]") {
		t.Errorf("an undefined name did not expand away:\n  %v", got)
	}

	if !slices.Contains(got, "Y=[$KEPT]") {
		t.Errorf("$$ did not escape the expansion:\n  %v", got)
	}
}
