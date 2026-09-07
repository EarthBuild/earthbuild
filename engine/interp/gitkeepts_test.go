package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `GIT CLONE --keep-ts` is accepted, because it asks for what already happens.
//
// The flag says: keep the checkout's file timestamps rather than overwriting
// them with a constant. This engine keeps timestamps everywhere - a capture
// records them to the nanosecond (I8) - so a clone with the flag and one
// without produce the same tree. `COPY --keep-ts` and `SAVE ARTIFACT --keep-ts`
// are already accepted for exactly this reason; this one was left out.
//
// Refusing a flag while doing what it asks is the expensive direction: it turns
// away a working Earthfile and tells its author the opposite of the truth.
// flagMeanings records that this mistake has been made here once before.
//
// **Stated as "the flag changes nothing" rather than "the build succeeds"**,
// because this harness resolves plans without fetching, so every clone is
// refused for a reason that has nothing to do with the flag. Comparing the two
// outcomes asks about the flag and only the flag - and it keeps asking if the
// harness ever learns to fetch.
func TestAskingAGitCloneToKeepTimestampsChangesNothing(t *testing.T) {
	t.Parallel()

	const (
		with = `
main:
    FROM alpine:3.22
    GIT CLONE --keep-ts https://example.invalid/r.git /src
`
		without = `
main:
    FROM alpine:3.22
    GIT CLONE https://example.invalid/r.git /src
`
	)

	gotWith, errWith := interp.Build(versioned+with, testMain)
	gotWithout, errWithout := interp.Build(versioned+without, testMain)

	switch {
	case errWith == nil && errWithout == nil:
		if len(gotWith.Graph.Nodes()) != len(gotWithout.Graph.Nodes()) {
			t.Errorf("--keep-ts changed the graph: %d nodes against %d",
				len(gotWith.Graph.Nodes()), len(gotWithout.Graph.Nodes()))
		}
	case errWith != nil && errWithout != nil:
		// Same refusal, for the same reason, which is not the flag.
		if errWith.Error() != errWithout.Error() {
			t.Errorf("--keep-ts changed the refusal:\n  with:    %v\n  without: %v",
				errWith, errWithout)
		}

		if strings.Contains(errWith.Error(), "keep-ts") {
			t.Errorf("the clone is refused over the flag: %v", errWith)
		}
	default:
		t.Errorf("--keep-ts decided whether the clone planned at all:"+
			"\n  with:    %v\n  without: %v", errWith, errWithout)
	}
}
