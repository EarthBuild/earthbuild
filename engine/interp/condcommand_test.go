package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A condition holding a command is not decided by comparing its text.
//
// `branch` expands arguments and then asks `decide`, which compares tokens as
// strings. `[ "$(echo yes)" = "yes" ]` is two different strings, so `decide`
// answered false - not "I cannot tell", false - and the fallback that runs a
// condition it cannot decide never fired. The branch was skipped, nothing was
// printed, and the build exited 0 having done less than the Earthfile said.
// earthly runs the same file and takes the branch (E786).
//
// Asserted as an error here because deciding it needs a runner and this test
// has none: the point is that the engine now says it cannot tell, where before
// it said no.
func TestAConditionHoldingACommandIsNotDecidedAsText(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(`VERSION 0.8
FROM alpine:3.24.1
t:
    IF [ "$(echo yes)" = "yes" ]
        RUN echo taken
    END
`, "t")
	if err == nil {
		t.Fatal("a condition needing a command was decided without one, which is" +
			" how a branch gets skipped in silence")
	}

	if !strings.Contains(err.Error(), "needs to run") {
		t.Errorf("the refusal does not say the condition needs running: %v", err)
	}
}

// A condition that is only text is still decided without running anything.
//
// The fallback costs a step, so it must not be taken for `[ "yes" = "yes" ]` -
// which is most conditions, and which a build with no runner has always been
// able to plan.
func TestAConditionThatIsOnlyTextIsStillDecidedHere(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(`VERSION 0.8
FROM alpine:3.24.1
t:
    IF [ "yes" = "yes" ]
        RUN echo taken
    END
`, "t")
	if err != nil {
		t.Fatalf("a plain condition now needs a runner: %v", err)
	}
}
