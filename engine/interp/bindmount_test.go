package interp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A bind mount from the host is refused on purpose, not for want of building it.
//
// `RUN --mount=type=bind-experimental,source=/bind-test,target=/bind` gives a
// step a **writable window onto the machine running the build**, and
// `tests/host-bind.earth` writes through it: `echo "hello b" > /bind/b.txt`.
//
// That is the thing this engine has already decided about, twice, in other
// words: a step's writes are held to its own layer (green paper A3), and
// `SAVE ARTIFACT --force` is refused because "this engine never writes outside
// the project". A host bind is the same hazard arriving by a different door, and
// refusing it is a position rather than a gap (E485).
//
// The sentinel is what says which. It was refused as *unimplemented*, so both
// sweeps counted it as work somebody should do - and the work is a decision
// somebody would have to reverse.
func TestABindMountFromTheHostIsRefusedOnPurpose(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n"+
		"    RUN --mount=type=bind-experimental,target=/bind,source=/bind-test ls /bind\n",
		testMain)
	if err == nil {
		t.Fatal("a step was given a writable window onto the host")
	}

	if !errors.Is(err, interp.ErrOnPurpose) {
		t.Errorf("refused with %q\n  which is marked as work left to do rather"+
			" than as the decision it is", err)
	}

	// And it says what to do instead, because a decision the reader cannot work
	// around is a decision that reads as a bug.
	for _, want := range []string{"bind-experimental", "COPY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refused with %q, which does not mention %q", err, want)
		}
	}
}

// A mount type nobody has heard of is still a gap rather than a decision.
//
// The other direction, and what keeps the label meaning something: this engine
// has no position on `type=tmpfs`, it simply does not have one, and saying
// "on purpose" about everything it cannot do would make the word useless.
func TestAnUnknownMountTypeIsStillAGap(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n"+
		"    RUN --mount=type=tmpfs,target=/t ls /t\n", testMain)
	if err == nil {
		t.Fatal("an unknown mount type was accepted")
	}

	if !errors.Is(err, interp.ErrUnimplemented) {
		t.Errorf("refused with %q, and this engine has no position on tmpfs -"+
			" it just has not built it", err)
	}
}
