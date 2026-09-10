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

// canHold answers what layers a store has and what a base holds, and nothing
// else - which is every executor that existed before the staleness question
// did.
type canHold struct{}

func (canHold) StoreHas(context.Context, []ir.NodeID) ([]ir.NodeID, error) { return nil, nil }

func (canHold) ViewDigests(
	context.Context, []ir.NodeID, []string,
) (map[string]ir.NodeID, map[string]ir.NodeID, error) {
	return nil, nil, nil
}

// canAlsoAnswer can be asked the whole question.
type canAlsoAnswer struct{ canHold }

func (canAlsoAnswer) WhyStaleIn(
	context.Context, []ir.NodeID, core.Observation,
) (string, error) {
	return "", nil
}

// TestAnExecutorThatCannotAnswerStalenessKeepsItsGuestStore.
//
// **The regression this separation exists for.** `WhyStaleIn` was once added to
// the single assertion that decides whether the layer store lives in the guest.
// Only the guest client had the method, so the assertion failed, the host kept
// the store, no layer was ever transferred into the sandbox, and every base the
// guest tried to materialise was missing - the corpus went from 194 of 246 to
// 88. The line that named it was one nobody reads: "this executor cannot be
// asked what it holds, so this build caches nothing".
//
// An interface assertion that gains a method silently loses a capability, and
// the capability it loses is not the one being added.
func TestAnExecutorThatCannotAnswerStalenessKeepsItsGuestStore(t *testing.T) {
	t.Parallel()

	asker, faster := guestStoreAskers(canHold{})
	if asker == nil {
		t.Fatal("an executor that can say what its store holds was refused one," +
			" so its build transfers no layer into the guest and caches nothing")
	}

	if faster != nil {
		t.Error("an executor with no WhyStaleIn was reported as having one")
	}
}

// TestAnExecutorThatCanAnswerStalenessIsAskedTo.
func TestAnExecutorThatCanAnswerStalenessIsAskedTo(t *testing.T) {
	t.Parallel()

	asker, faster := guestStoreAskers(canAlsoAnswer{})
	if asker == nil || faster == nil {
		t.Fatalf("an executor that can answer both was offered %v and %v", asker, faster)
	}
}

// TestAnExecutorThatCanDoNeitherGetsNoGuestStore. The requirement is a
// requirement: a store nobody can be asked about must not be treated as one
// held in the guest.
func TestAnExecutorThatCanDoNeitherGetsNoGuestStore(t *testing.T) {
	t.Parallel()

	if asker, _ := guestStoreAskers(struct{}{}); asker != nil {
		t.Error("an executor that cannot be asked what it holds was treated as" +
			" holding the store in its guest")
	}
}
