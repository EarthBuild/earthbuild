package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// The two languages mean different things by "bind", and get different answers.
//
// An Earthfile's `type=bind-experimental` takes a **host** path and a step
// writes through it - `tests/host-bind.earth` does exactly that. This engine has
// decided against that twice in other words: a step's writes are held to its own
// layer (A3), and `SAVE ARTIFACT --force` is refused because nothing is written
// outside the project. So it is refused *on purpose*, and always will be.
//
// A Dockerfile's `type=bind` is neither of those things. It is a read-only view
// of the build context, or of an earlier stage - content this build already has
// and already digests. Nothing about it is a window onto the machine, so the
// decision does not reach it: it is simply not built yet, which makes it work
// somebody could do rather than a position somebody would have to reverse.
//
// The distinction is exact, not a guess: the shipping engine accepts only
// `bind-experimental` in an Earthfile (earthfile2llb/runmount.go), so a plain
// `type=bind` reaching this parser came from a Dockerfile.
//
// It matters because the sentinel is what the corpus sweeps count. Filed as a
// decision, 371 targets look like a settled question; filed as a gap, they are
// the largest piece of remaining work, which is what they are.
func TestTheTwoKindsOfBindAreAnsweredDifferently(t *testing.T) {
	t.Parallel()

	host := refusalOf(t, `
main:
    FROM alpine:3.22
    RUN --mount=type=bind-experimental,target=/b,source=/tmp/x true
`)
	dockerfileKind := refusalOf(t, `
main:
    FROM alpine:3.22
    RUN --mount=type=bind,source=.,target=/b true
`)

	if host == nil {
		t.Fatal("a host bind was accepted; it is refused by decision")
	}

	// "by design" is the wording refusedOnPurpose uses; a gap does not carry it.
	if !strings.Contains(host.Error(), "design") {
		t.Errorf("a host bind is not refused as a decision: %v", host)
	}

	// **And the other one is built now**, which is the other half of the same
	// point: the decision was about a writable window onto the machine, and it
	// never reached a read-only view of content this build already has.
	if dockerfileKind != nil {
		t.Errorf("a view of the build context was refused: %v", dockerfileKind)
	}
}

// refusalOf builds and returns the error, or nil when it planned.
func refusalOf(t *testing.T, src string) error {
	t.Helper()

	_, err := interp.Build(versioned+src, testMain)

	return err
}
