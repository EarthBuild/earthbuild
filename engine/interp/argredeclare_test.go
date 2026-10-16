package interp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// Declaring one argument twice in a recipe is an error.
//
// `tests/arg-redeclare-error.earth` is named for it and has two targets that
// exist to be refused:
//
//	test-error-conflict:
//	    ARG FOO
//	    ARG FOO
//
// This engine kept the first value and carried on (E438), which is the right
// answer to *which value wins* and the wrong answer to *whether this is an
// Earthfile*. The tree says it is not, and until the gate started reading
// `--should_fail` there was nothing to notice (E456).
//
// A redeclaration is almost always a mistake - a name typed twice, or a copied
// block - and the second one silently doing nothing is how an author's intended
// default never takes effect.
func TestDeclaringAnArgumentTwiceIsAnError(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG FOO\n    ARG FOO\n    RUN echo $FOO\n",
		testMain)
	if err == nil {
		t.Fatal("a target declaring one argument twice planned")
	}

	for _, want := range []string{"FOO", "Earthfile:6"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refused with %q, which does not mention %q", err, want)
		}
	}
}

// Inside an IF is still the same recipe.
//
// The corpus's second case, and the one a naive fix misses: a branch is not a
// new scope for arguments, so the declaration inside it conflicts with the one
// above just as a flat repetition would.
func TestRedeclaringInsideABranchIsAlsoAnError(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG FOO\n"+
		"    IF true\n        ARG FOO\n    END\n    RUN echo $FOO\n", testMain)
	if err == nil {
		t.Fatal("a target declaring one argument twice, once inside an IF, planned")
	}
}

// A target may still override a global.
//
// The distinction the fix rests on: `ARG --global FOO` declares in one scope and
// `ARG FOO` in another, so a target that overrides an inherited global is not
// redeclaring anything - and the same corpus file asserts that two targets
// later, which is what makes this a pair rather than a rule with an exception.
func TestOverridingAGlobalIsNotARedeclaration(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nFROM alpine:3.22\nARG --global FOO = bar\n\n"+
		"main:\n    ARG FOO = baz\n    RUN echo $FOO\n")

	if !strings.HasSuffix(got, "echo baz") {
		t.Errorf("the step runs %q; a target's own default overrides a global", got)
	}
}

// And the base recipe may declare a name it has already made global.
//
// `tests/arg-redeclare-error.earth` opens with exactly this and expects it to
// build - the global keeps its value (E438) - so the rule is *one declaration
// per name per scope*, not one per name.
func TestTheBaseRecipeMayDeclareAGlobalAndThenALocal(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nFROM alpine:3.22\nARG --global FOO = bar\nARG FOO = bacon\n\n"+
		"main:\n    RUN echo $FOO\n")

	if !strings.HasSuffix(got, "echo bar") {
		t.Errorf("the step runs %q, and the global's value stands", got)
	}
}

// A required argument may not have a default.
//
// `tests/required-args.earth` has a target that exists to be refused for it:
//
//	ARG --required shouldNotHaveDefaultValue=default
//
// The two words contradict each other. `--required` says the build must not
// proceed without a value from the caller; a default says it always has one - so
// the flag can never fire, and an author who wrote both meant one of them (E470).
//
// Refused rather than resolved in either direction: dropping the default would
// build something the author did not write, and dropping the flag would let a
// build proceed that they said must not.
func TestARequiredArgumentTakesNoDefault(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG --required v=default\n    RUN echo $v\n",
		testMain)
	if err == nil {
		t.Fatal("`ARG --required v=default` planned, and the flag can never fire")
	}

	for _, want := range []string{"--required", "v"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refused with %q, which does not mention %q", err, want)
		}
	}
}

// And one without a default still asks the caller for a value.
//
// The control: this is the shape `--required` is for, and refusing it would be
// removing the feature rather than the contradiction.
func TestARequiredArgumentWithoutOneStillAsks(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG --required v\n    RUN echo $v\n",
		testMain)
	if err == nil {
		t.Fatal("a required argument nobody supplied was passed over")
	}

	if !errors.Is(err, interp.ErrNotProvided) {
		t.Errorf("refused with %q, and a value the caller did not pass is a"+
			" withheld value rather than a broken Earthfile", err)
	}
}
