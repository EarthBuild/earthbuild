package cli

import (
	"errors"
	"fmt"
	"testing"
)

// The reason survives the sentinel.
//
// `ErrNotDerivable` wraps a specific reason - which step, and what about it -
// and the sentinel's own text says only that there is one. `wouldSkipPlan`
// stripped the prefix and printed the reason; `noteBuild` called
// `errors.Unwrap`, which yields the sentinel and discards exactly the half a
// reader needs. A cold substrate build therefore reported "this build's inputs
// cannot be derived without running it" and never said which step or why.
func TestTheReasonSurvivesTheSentinel(t *testing.T) {
	t.Parallel()

	const reason = "Earthfile:202 ran and was not watched"

	if got := reasonOf(fmt.Errorf("%w: %s", ErrNotDerivable, reason)); got != reason {
		t.Errorf("reported %q, want %q - the specific half was discarded", got, reason)
	}

	// A bare sentinel has no reason to give, and must not report an empty one.
	if got := reasonOf(ErrNotDerivable); got != ErrNotDerivable.Error() {
		t.Errorf("a bare sentinel reported %q", got)
	}

	// Anything else is passed through whole: it is not ours to reformat.
	other := errors.New("the store is unreadable")
	if got := reasonOf(other); got != other.Error() {
		t.Errorf("an unrelated error reported %q", got)
	}
}
