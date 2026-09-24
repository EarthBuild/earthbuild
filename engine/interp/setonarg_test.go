package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestSetRefusesAnArgAndSaysHowToFixIt.
//
// `SET` reassigns what `LET` introduced. An `ARG` is the target's *interface* -
// a caller may override it, and a build reading the same Earthfile with
// different arguments is a different build - so writing to one from inside
// would make the value depend on where in the recipe you looked.
//
// `tests/arg-set.earth` exists to be refused, and the corpus pins the wording
// as well as the refusal: `--output_contains="Hint: 'foo' is an ARG and cannot
// be used with SET - try declaring 'LET foo = \$foo' first"`. The hint is the
// useful half - it names the one line that makes the rest legal - so it is
// asserted rather than only the failure.
//
// Found by the corpus after `LET`/`SET` stopped needing a feature flag at 0.8:
// the file had been refused for want of the flag, which was the right answer
// for the wrong reason, and enabling the construct revealed that the rule
// underneath it was never implemented.
func TestSetRefusesAnArgAndSaysHowToFixIt(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG foo
    SET foo = bar
    RUN echo $foo
`, testMain)
	if err == nil {
		t.Fatal("SET wrote to an ARG; a caller overriding it would then find" +
			" the value changed under them halfway down the recipe")
	}

	for _, want := range []string{"'foo' is an ARG", "SET", "LET foo = $foo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refused with %q, which does not carry %q", err, want)
		}
	}

	// **And the hint has to work**, which is the half that makes the rule
	// usable: `LET foo = $foo` re-introduces the name as a variable of the
	// recipe, after which SET is the ordinary thing to do with it.
	// `tests/cli/testdata/let-set/Earthfile` is written exactly that way -
	// `ARG foo = sports`, `LET foo = ${foo}`, `SET foo = $foo car` - and a rule
	// that refused it would refuse its own advice.
	_, err = interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG foo = sports
    LET foo = ${foo}
    SET foo = $foo car
    RUN test "$foo" = "sports car"
`, testMain)
	if err != nil {
		t.Errorf("the fix the hint recommends was refused: %v", err)
	}

	// A LET-declared name is exactly what SET is for, and still works.
	_, err = interp.Build(versioned+`
main:
    FROM alpine:3.22
    LET foo = one
    SET foo = two
    RUN echo $foo
`, testMain)
	if err != nil {
		t.Errorf("SET on a LET is the ordinary use and was refused: %v", err)
	}
}
