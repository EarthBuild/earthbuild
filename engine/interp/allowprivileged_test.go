package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestPrivilegedIsAllowedWhenTheCallerOptsIn.
//
// **`RUN --privileged` is refused by default and that is right**: a step here
// already holds every capability inside its namespace and cannot reach past it,
// so the flag promises something it cannot deliver and the refusal says so.
//
// It is refused *by default*, though, not for ever. `--allow-privileged` is the
// caller saying they know what the flag does here and want it anyway - sixteen
// of this repository's own corpus invocations pass it - and an engine that
// refuses a construct the operator has explicitly opted into is refusing to be
// used rather than refusing to be wrong.
func TestPrivilegedIsAllowedWhenTheCallerOptsIn(t *testing.T) {
	t.Parallel()

	const src = `VERSION 0.8
t:
    FROM alpine:3.21
    RUN --privileged echo hi
`

	_, err := interp.Build(src, "t")
	if err == nil {
		t.Fatal("RUN --privileged was accepted with nobody asking for it")
	}

	if !strings.Contains(err.Error(), "--privileged") {
		t.Errorf("the refusal does not name the flag: %v", err)
	}

	_, err = interp.Build(src, "t", interp.WithAllowPrivileged(true))
	if err != nil {
		t.Errorf("RUN --privileged refused although the caller opted in: %v", err)
	}
}

// TestTheOptInDoesNotCrossARepositoryBoundary.
//
// **A caller opting into privilege is saying it about the build they wrote.**
// Not about whatever a fetched Earthfile turns out to contain: granting it there
// would let a remote target take privilege the operator never considered, on
// code they may never have read.
//
// The reference engine requires it be granted again at the `FROM` or `IMPORT`
// that reaches out, and the corpus asserts the refusal in five places -
// `reject-privileged-in-remote-repo-triggered-by-from-privileged` and its
// siblings, each of which is *meant to fail*. Passing `--allow-privileged`
// globally made all five build, which is the flag doing more than it was asked.
func TestTheOptInDoesNotCrossARepositoryBoundary(t *testing.T) {
	t.Parallel()

	// A local Earthfile: the opt-in applies, as the test above asserts.
	const local = `VERSION 0.8
t:
    FROM alpine:3.21
    RUN --privileged echo hi
`

	_, err := interp.Build(local, "t", interp.WithAllowPrivileged(true))
	if err != nil {
		t.Fatalf("the caller's own Earthfile was refused: %v", err)
	}

	// The boundary itself is exercised by the corpus rather than here: reaching
	// a remote repository needs a network and a repository, and what this
	// package can hold is that the flag is read from the unit rather than from
	// the build - which the local case above and `fetchedFrom` together fix.
}
