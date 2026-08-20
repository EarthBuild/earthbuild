package interp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `ARG --global NAME=value` declares NAME, not `--global`.
//
// The flags were being read as the argument's name, so `ARG --global
// IMAGE_REGISTRY=...` declared an argument called `--global` and left
// IMAGE_REGISTRY undeclared - which then surfaced somewhere else entirely, as
// an IF that "tests an argument that is not declared". The root Earthfile of
// this repository opens with exactly that line.
func TestArgFlagsAreNotTheArgumentName(t *testing.T) {
	t.Parallel()

	// Each declaration where its flags say it belongs: a `--global` in the base
	// recipe, because a target that declares one is refused (E461), and a plain
	// one in the target, because a base recipe's local does not reach a target
	// (E438). The two rules together decide the placement, and neither is what
	// this test is about - the flags are not the name, wherever the line is.
	for _, tc := range []struct{ decl, want string }{
		{"ARG --global GREETING=hello", testGreeting},
		{"ARG GREETING=hello", testGreeting},
		// `--required` with a default is a contradiction and is refused (E470),
		// so the pair this asserts is `--global --required` without one - which
		// is still two flags before a name, which is what this test is about.
		{"ARG --global --required GREETING", ""},
	} {
		t.Run(tc.decl, func(t *testing.T) {
			t.Parallel()

			src := versioned + "\nmain:\n    FROM alpine:3.22\n    " +
				tc.decl + "\n    RUN echo $GREETING\n"
			if strings.Contains(tc.decl, "--global") {
				src = versioned + "\nFROM alpine:3.22\n" + tc.decl +
					"\n\nmain:\n    RUN echo $GREETING\n"
			}

			p, err := interp.Build(src, testMain)
			if tc.want == "" {
				// A required argument nobody supplied: the declaration is what
				// this test is about, and the refusal proves the name was read
				// as GREETING rather than as a flag.
				if err == nil || !strings.Contains(err.Error(), "GREETING") {
					t.Fatalf("refused with %v, and the name is GREETING", err)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if got := describe(p.Graph.Nodes()); !strings.Contains(got, tc.want) {
				t.Errorf("the argument did not reach the command:\n%s", got)
			}
		})
	}
}

// `ARG --required NAME` with nothing supplied is refused, saying so.
//
// It is declared - that is what the line does - and it has no value, and
// --required is the author saying the build must not proceed without one. The
// old message said it was "not a declared argument", which sent the reader to
// add a declaration that was already there.
func TestARequiredArgumentWithNoValueSaysWhichOneAndWhy(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG --required TOKEN\n    RUN use $TOKEN\n", testMain)
	if err == nil {
		t.Fatal("a required argument with no value was accepted")
	}

	for _, want := range []string{testSecret, "required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}

	if strings.Contains(err.Error(), "not a declared argument") {
		t.Errorf("it reports a declared argument as undeclared:\n%s", err)
	}
}

// A required argument that is supplied is simply used.
func TestARequiredArgumentIsSatisfiedByAValue(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG --required TOKEN\n    RUN use $TOKEN\n",
		testMain, interp.WithArgs(map[string]string{testSecret: "secret-value"}))
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "secret-value") {
		t.Errorf("the supplied value did not reach the command:\n%s", got)
	}
}

// A missing required argument is a capability the caller withheld, not an
// invalid Earthfile.
//
// `ErrNotProvided` exists because the corpus report is read to decide what to
// build next, and "a construct that is finished but unavailable to a plan-only
// caller has no business at the top of it". A `--required` ARG is exactly that
// shape: **the Earthfile is valid** - declaring an argument the invocation must
// supply is the feature working - and it is the invocation that is incomplete.
// `ErrNoRunner` is the sibling case of "must run something to know".
//
// The rule was applied to probes and fetches and not here, so the corpus put
// these under "refused as invalid input: verify these are right". They are
// right, and they are not invalid input. E111's shape: a rule applied at one of
// the two places it holds.
//
// Refusal is unaffected - this classifies an error, it does not soften one, and
// the test above still requires the message to name the argument and say how to
// pass it.
func TestAMissingRequiredArgumentIsAWithheldValueNotABadEarthfile(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG --required TOKEN\n    RUN use $TOKEN\n", testMain)
	if err == nil {
		t.Fatal("a required argument with no value was accepted")
	}

	if !errors.Is(err, interp.ErrNotProvided) {
		t.Errorf("a value the caller did not supply is not in the withheld family,"+
			" so the corpus counts it as an invalid Earthfile:\n%s", err)
	}

	// Not the runner case. A value is passed on the command line; nothing has
	// to be executed to learn it, and merging the two would have `--engine=
	// buildkit` offered as the remedy for a forgotten flag.
	if errors.Is(err, interp.ErrNoRunner) {
		t.Errorf("a forgotten flag is reported as needing something to run:\n%s", err)
	}
}

// An unsupplied secret is a withheld value too, by the same reasoning.
//
// The third place the rule holds - after a probe to run and a repository to
// fetch. `RUN --secret TOKEN=t` in an Earthfile that never receives `--secret`
// is a valid Earthfile and an incomplete invocation, and both spellings say so:
// the `--secret` flag and a `--mount=type=secret`.
//
// Found by reading the corpus report *after* the argument fix landed: with
// eighty causes of one kind removed, the two secret rows became legible. A list
// dominated by one class hides the others, which is the whole argument for
// classifying rather than counting messages.
func TestAnUnsuppliedSecretIsAWithheldValue(t *testing.T) {
	t.Parallel()

	for name, src := range map[string]string{
		"flag":  "\nmain:\n    FROM alpine:3.22\n    RUN --secret TOKEN=api-token echo $TOKEN\n",
		"mount": "\nmain:\n    FROM alpine:3.22\n    RUN --mount=type=secret,id=api-token,target=/t cat /t\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+src, testMain)
			if err == nil {
				t.Fatal("a secret nobody supplied was accepted")
			}

			if !errors.Is(err, interp.ErrNotProvided) {
				t.Errorf("a secret the caller did not supply is not in the withheld"+
					" family, so the corpus counts it as an invalid Earthfile:\n%s", err)
			}
		})
	}
}
