package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// The engine's own names are not the author's to set.
//
// Three of the ten targets this engine built where the tree says it must not,
// and they are one rule: a name the engine supplies cannot be supplied by the
// Earthfile, because then two things claim to say what it means (E457).
//
//   - `LABEL dev.earthly.foo=bar` writes in the namespace the engine stamps its
//     own labels in, where a reader cannot tell an engine's statement about the
//     image from the author's.
//   - `ARG EARTHLY_VERSION="this is not possible"` gives a default to an
//     argument the engine answers. The default can never apply - which is the
//     kind interpretation - and an author writing one has misunderstood
//     something the build should say out loud.
//   - `BUILD +t --EARTHLY_VERSION=...` is the same mistake from the calling
//     side, and this one *can* take effect, which makes it the dangerous member:
//     a target would be built against a version string its caller invented.
func TestAReservedLabelIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    LABEL dev.earthly.foo=bar\n", testMain)
	if err == nil {
		t.Fatal("an Earthfile wrote a label in the engine's own namespace")
	}

	for _, want := range []string{"dev.earthly", "Earthfile:5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refused with %q, which does not mention %q", err, want)
		}
	}
}

// An ordinary label is still an ordinary label.
//
// Asserted beside it, because a check that refused every label would be a
// missing feature with a security-shaped explanation.
func TestAnOrdinaryLabelIsFine(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    LABEL com.example.foo=bar\n", testMain)
	if err != nil {
		t.Fatalf("an ordinary label was refused: %v", err)
	}
}

// A builtin argument cannot be given a default.
func TestABuiltinArgumentTakesNoDefault(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG EARTHLY_VERSION=\"not possible\"\n"+
		"    RUN echo $EARTHLY_VERSION\n", testMain)
	if err == nil {
		t.Fatal("an Earthfile gave a default to an argument the engine supplies")
	}

	if !strings.Contains(err.Error(), "EARTHLY_VERSION") {
		t.Errorf("refused with %q, which does not name the argument", err)
	}
}

// Declaring one without a default is how you ask for it, and stays legal.
func TestDeclaringABuiltinArgumentIsHowYouReadIt(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG EARTHLY_TARGET_NAME\n"+
		"    RUN echo [$EARTHLY_TARGET_NAME]\n")

	if !strings.Contains(got, "[main]") {
		t.Errorf("the step runs %q, and declaring a builtin is how it is read", got)
	}
}

// And a caller cannot pass one either.
//
// The dangerous member of the family: unlike a default, a passed value *can*
// take effect, so a target would be built against a version string its caller
// invented.
func TestABuiltinArgumentCannotBePassed(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\ndep:\n    FROM alpine:3.22\n    ARG EARTHLY_VERSION\n    RUN echo $EARTHLY_VERSION\n"+
		"\nmain:\n    FROM alpine:3.22\n    BUILD +dep --EARTHLY_VERSION=\"not possible\"\n",
		testMain)
	if err == nil {
		t.Fatal("a caller passed a value for an argument the engine supplies")
	}

	if !strings.Contains(err.Error(), "EARTHLY_VERSION") {
		t.Errorf("refused with %q, which does not name the argument", err)
	}
}
