package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestAGuestViewCanBeAskedTheStalenessQuestion.
//
// **A switch wired to nothing.** `EARTH_ASK_STALE=1` reaches
// `core.whyStaleVia`, which asks the view source for `WhyStaleIn` and falls
// back to fetching every digest when the source has not got one. The source a
// guest store gets is this one, and it had `View` and `ViewFor` and no
// `WhyStaleIn` - so the setting turned on and changed nothing, and the tier went
// on hashing 6302 files to find the one that moved.
//
// Measured on the step that builds this repository: that lookup is 4.409s in a
// microVM against 0.222s on the host, 20 times the cost per file, because a
// freshly booted guest reads them all off its device with an empty page cache.
//
// The interface, not the timing: a benchmark would show this as noise on a busy
// machine, while an absent method is an absence at any load.
func TestAGuestViewCanBeAskedTheStalenessQuestion(t *testing.T) {
	t.Parallel()

	var views core.ViewSource = &guestViews{}

	if _, ok := views.(core.StaleAsker); !ok {
		t.Fatal("a guest's view source cannot be asked whether an observation is" +
			" stale, so EARTH_ASK_STALE turns on and the tier still fetches every" +
			" digest it was meant to stop fetching")
	}
}

// TestTheStalenessQuestionGoesToTheGuest checks the wiring carries the question
// rather than merely satisfying the interface.
func TestTheStalenessQuestionGoesToTheGuest(t *testing.T) {
	t.Parallel()

	var (
		asked []ir.NodeID
		want  = []ir.NodeID{{1}, {2}}
	)

	views := &guestViews{
		stale: func(_ context.Context, stack []ir.NodeID, _ core.Observation) (string, error) {
			asked = stack

			return "/bin/busybox is gone from the base", nil
		},
	}

	why, err := views.WhyStaleIn(t.Context(), want, core.Observation{})
	if err != nil {
		t.Fatal(err)
	}

	if why != "/bin/busybox is gone from the base" {
		t.Errorf("the guest's answer did not come back: %q", why)
	}

	if len(asked) != len(want) {
		t.Errorf("asked about %d layers, wanted %d", len(asked), len(want))
	}
}

// TestAViewWithNoAskerDeclines. A source built for a store the host can read
// has no guest to ask, and must say so rather than answer "nothing is stale".
func TestAViewWithNoAskerDeclines(t *testing.T) {
	t.Parallel()

	views := &guestViews{}

	_, err := views.WhyStaleIn(t.Context(), []ir.NodeID{{1}}, core.Observation{})
	if !errors.Is(err, errNoStaleAsker) {
		t.Errorf("a view with nobody to ask answered anyway: %v", err)
	}
}
