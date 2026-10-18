package interp_test

import (
	"strings"
	"testing"
)

// `EARTHLY_VERSION` and `EARTHLY_BUILD_SHA` are never empty.
//
// `tests/builtin-args.earth` asserts `test -n` on both, which is the weakest
// possible assertion and this engine failed it: neither was supplied, so each
// declared itself and expanded to nothing.
//
// They say which engine built the image, and an Earthfile that stamps a label
// with one gets an empty label - a build whose provenance is missing, reported
// as a success (E448).
//
// The strings are injected at link time and are empty in a `go test` binary and
// in `go run`, which is the case that matters: **a value that is only correct in
// a release build is a value that is wrong every time a developer looks at it**.
// So the fallback is a real answer rather than the empty string.
func TestTheEngineNamesItselfAndItsBuild(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n"+
		"    ARG EARTHLY_VERSION\n    ARG EARTHLY_BUILD_SHA\n"+
		"    RUN echo [$EARTHLY_VERSION] [$EARTHLY_BUILD_SHA]\n")

	for _, empty := range []string{"[]", "[] ["} {
		if strings.Contains(got, empty) {
			t.Fatalf("the step runs %q, and neither argument may be empty", got)
		}
	}
}

// The same value, whichever spelling is asked for.
//
// `EARTH_VERSION` is the new name and `EARTHLY_VERSION` the one every existing
// Earthfile uses; supplying different answers to the two would be a rename this
// project did to other people's files, which is the rule the rest of the family
// already follows.
func TestBothSpellingsOfTheVersionAgree(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nmain:\n    FROM alpine:3.22\n"+
		"    ARG EARTH_VERSION\n    ARG EARTHLY_VERSION\n"+
		"    RUN echo [$EARTH_VERSION] [$EARTHLY_VERSION]\n")

	// Split on the brackets, not on spaces: the version may contain a space and
	// the first version of this test cut on one, so it compared "[earthbuild-"
	// against "native" and failed against two identical values.
	inside := strings.Split(got, "] [")
	if len(inside) != 2 || strings.TrimPrefix(inside[0], "/bin/sh -c echo [") !=
		strings.TrimSuffix(inside[1], "]") {
		t.Errorf("the step runs %q, and the two spellings name one engine", got)
	}
}
