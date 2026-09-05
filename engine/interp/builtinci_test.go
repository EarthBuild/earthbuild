package interp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `EARTHLY_CI` says whether this is a CI build, and says one of two things.
//
// `tests/ci-arg.earth` asserts it: `test "$EARTHLY_CI" = "true" || test
// "$EARTHLY_CI" = "false"`. This engine supplied nothing, so the argument
// declared itself and expanded to the empty string - which is neither, and the
// target failed at execution with `test "" = "true"` (E443).
//
// Empty is the wrong answer twice over: it is not a value the flag can take, and
// it reads in a shell exactly like "not set", so an Earthfile branching on it
// takes the local path on a CI machine and nothing says why.
func TestTheCIArgumentSaysTrueOrFalse(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG EARTHLY_CI\n    RUN echo [$EARTHLY_CI]\n")

	if !strings.HasSuffix(got, "[true]") && !strings.HasSuffix(got, "[false]") {
		t.Errorf("the step runs %q; EARTHLY_CI is true or false, never empty", got)
	}
}

// It is read from the environment, which is where CI says so.
//
// Every CI system this engine is likely to run under sets `CI` in the
// environment; that is the convention the reference follows too. Read once, at
// the top, so two steps of one build cannot disagree.
func TestTheCIArgumentFollowsTheEnvironment(t *testing.T) {
	t.Setenv("CI", "true")

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG EARTHLY_CI\n    RUN echo [$EARTHLY_CI]\n")

	if !strings.HasSuffix(got, "[true]") {
		t.Errorf("the step runs %q with CI=true in the environment", got)
	}
}

// `EARTHLY_SOURCE_DATE_EPOCH` is 0 unless something says otherwise.
//
// `tests/builtin-args.earth` asserts `test "$EARTHLY_SOURCE_DATE_EPOCH" = "0"`.
// It is the timestamp a reproducible build stamps its files with, and the
// default is what makes two builds of the same tree produce the same bytes - so
// an engine that leaves it empty leaves every file it writes with whatever the
// clock said.
func TestTheSourceDateEpochDefaultsToZero(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG EARTHLY_SOURCE_DATE_EPOCH\n"+
		"    RUN echo [$EARTHLY_SOURCE_DATE_EPOCH]\n")

	if !strings.HasSuffix(got, "[0]") {
		t.Errorf("the step runs %q; the default is 0", got)
	}
}

// And SOURCE_DATE_EPOCH from the environment wins, which is the point of it.
func TestTheSourceDateEpochFollowsTheEnvironment(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG EARTHLY_SOURCE_DATE_EPOCH\n"+
		"    RUN echo [$EARTHLY_SOURCE_DATE_EPOCH]\n")

	if !strings.HasSuffix(got, "[1700000000]") {
		t.Errorf("the step runs %q with SOURCE_DATE_EPOCH set", got)
	}
}

// An unset CI is `false`, and a target that branches on it takes that branch.
//
// This is what the corpus ratchet moved for. `examples/aws-sso/Earthfile` reads
//
//	ARG EARTHLY_CI
//	IF [ "$EARTHLY_CI" = "false" ]
//	  ARG --required sso_region
//
// and with the argument empty the condition was false, the branch was never
// entered, and the target planned - by skipping the branch the reference takes.
// Supplying `false` reaches the `--required` argument, which this caller did not
// pass, so two targets move from "planned" to "blocked for want of something the
// caller withheld" (E443).
//
// **Two fewer targets plan and the engine is more correct**, which is why the
// number is written down beside the reason: a ratchet that only ever goes up
// would have been an argument against fixing this.
func TestAnUnsetCIReachesTheBranchThatWantsAnArgument(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG EARTHLY_CI\n"+
		"    IF [ \"$EARTHLY_CI\" = \"false\" ]\n"+
		"        ARG --required needed\n"+
		"        RUN echo $needed\n"+
		"    END\n", testMain)
	if err == nil {
		t.Fatal("the branch was not entered, so EARTHLY_CI is not \"false\" here")
	}

	if !errors.Is(err, interp.ErrNotProvided) {
		t.Errorf("refused with %q, and a --required argument nobody passed is a"+
			" withheld value rather than a broken Earthfile", err)
	}
}

// `EARTHLY_PUSH` says whether this invocation is pushing, and says one of two
// things.
//
// `tests/push-arg.earth` asserts the pair, exactly as `ci-arg.earth` does for
// `EARTHLY_CI` (E443) - and for the same reason, because in a shell an empty
// string reads like *not set* and an Earthfile branching on it takes the wrong
// path with nothing to say why (E472).
//
// This engine has no push mode, so the answer is `false` - which is a fact about
// this invocation rather than a placeholder. When it has one, this is where the
// answer comes from.
func TestThePushArgumentSaysTrueOrFalse(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG EARTHLY_PUSH\n    RUN echo [$EARTHLY_PUSH]\n")

	if !strings.HasSuffix(got, "[false]") {
		t.Errorf("the step runs %q; this engine does not push, so it is false", got)
	}
}

// `EARTHLY_CI_RUNNER` exists only where the dialect asks for it.
//
// `tests/builtin-args.earth` asserts both halves: `test -z "$EARTHLY_CI_RUNNER"`
// under a plain VERSION line, and the value under
// `VERSION --earthly-ci-runner-arg`. A builtin that appeared regardless would
// answer a question the file never asked (E472).
//
// Its value says whether *this runner* is a CI runner, which is a fact about the
// invocation: read from the environment, `false` when nothing set it.
func TestTheCIRunnerArgumentIsGatedOnItsFeature(t *testing.T) {
	t.Parallel()

	// Without the flag it is an ordinary argument, and an ordinary argument
	// nobody supplied is empty.
	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG EARTHLY_CI_RUNNER\n"+
		"    RUN echo [$EARTHLY_CI_RUNNER]\n")

	if !strings.HasSuffix(got, "[]") {
		t.Errorf("the step runs %q, and the file did not ask for the builtin", got)
	}

	// With it, the engine answers.
	got = commandOfFirstExec(t, "VERSION --earthly-ci-runner-arg 0.8\n"+
		"\nmain:\n    FROM alpine:3.22\n    ARG EARTHLY_CI_RUNNER\n"+
		"    RUN echo [$EARTHLY_CI_RUNNER]\n")

	if !strings.HasSuffix(got, "[false]") {
		t.Errorf("the step runs %q, and nothing here is a CI runner", got)
	}
}

// And the environment is where the answer comes from.
func TestTheCIRunnerArgumentFollowsTheEnvironment(t *testing.T) {
	t.Setenv("EARTHLY_CI_RUNNER", "true")

	got := commandOfFirstExec(t, "VERSION --earthly-ci-runner-arg 0.8\n"+
		"\nmain:\n    FROM alpine:3.22\n    ARG EARTHLY_CI_RUNNER\n"+
		"    RUN echo [$EARTHLY_CI_RUNNER]\n")

	if !strings.HasSuffix(got, "[true]") {
		t.Errorf("the step runs %q with EARTHLY_CI_RUNNER=true set", got)
	}
}
