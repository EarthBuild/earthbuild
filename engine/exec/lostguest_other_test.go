//go:build !linux

package exec

import (
	"errors"
	"strings"
	"testing"
)

// failureSandbox is a sandbox that knows why its guest ended.
type failureSandbox struct {
	Sandbox

	why string
}

func (f failureSandbox) GuestFailure() string { return f.why }

// A lost guest on macOS says how the guest process ended.
//
// The Apple backend records the guest's exit status (GuestFailure) and nothing
// read it, so every death read `guest connection lost: EOF` - three rth reports
// in three weeks, none with a cause. The exit status is what tells a kill from
// a crash from a full disk.
func TestALostGuestSaysHowItEnded(t *testing.T) {
	t.Parallel()

	sb := failureSandbox{why: "the guest in this sandbox exited: signal: killed"}

	got := lostGuest(errors.New("unpack layer sha256:a9: guest connection lost: EOF"), sb)
	if !strings.Contains(got.Error(), "signal: killed") {
		t.Errorf("the lost guest does not say how it ended:\n%s", got)
	}

	// A step that merely failed leaves the guest well; its exit is not the story.
	plain := errors.New("RUN false exited 1")
	got = lostGuest(plain, sb)
	if got.Error() != plain.Error() {
		t.Errorf("an ordinary failure was decorated:\n%s", got)
	}
}
