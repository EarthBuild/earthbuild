package guest

import "testing"

// The guest keeps the first reason a step's filesystem was incomplete, and
// reports it to whoever asks.
//
// Separate from Degraded, which means one specific thing - why a step ran
// without the *limits* it was given. Putting a mount failure through that
// channel would corrupt a signal somebody reads, which is the argument the call
// sites made for dropping the reason entirely; a channel of its own is the
// answer they said was worth doing.
//
// **The first, not the last.** Forty steps fail the same mount for the same
// reason, and the fortieth reason is no better than the first while a later
// unrelated failure would replace a cause the reader still needs.
func TestTheGuestKeepsWhyAStepFilesystemWasIncomplete(t *testing.T) {
	t.Parallel()

	t.Run("nothing to say when every mount was made", func(t *testing.T) {
		t.Parallel()

		var s Server

		if got := s.Unmounted(); got != "" {
			t.Errorf("a guest that mounted everything reported %q", got)
		}
	})

	t.Run("keeps the first reason", func(t *testing.T) {
		t.Parallel()

		var s Server

		s.noteUnmounted("mount /sys for the step: operation not permitted")
		s.noteUnmounted("mount /sys/fs/cgroup for the step: no such file or directory")

		want := "mount /sys for the step: operation not permitted"
		if got := s.Unmounted(); got != want {
			t.Errorf("guest reported %q, want the first reason %q", got, want)
		}
	})

	t.Run("an empty reason is not a reason", func(t *testing.T) {
		t.Parallel()

		var s Server

		s.noteUnmounted("")

		if got := s.Unmounted(); got != "" {
			t.Errorf("an empty note became a reason: %q", got)
		}
	})
}

// The client carries the guest's reason back to the host, keeping the first.
//
// The guest and the host are different machines on macOS, so a reason that
// stays on the guest is a reason nobody reads - which is exactly how the
// unbounded warning was lost before E123.
func TestTheClientCarriesTheIncompleteReasonBack(t *testing.T) {
	t.Parallel()

	var c Client

	if got := c.Unmounted(); got != "" {
		t.Errorf("a fresh client reported %q", got)
	}

	c.noteUnmounted("")
	c.noteUnmounted("mount /sys for the step: operation not permitted")
	c.noteUnmounted("mount /sys/fs/cgroup for the step: no such file or directory")

	want := "mount /sys for the step: operation not permitted"
	if got := c.Unmounted(); got != want {
		t.Errorf("client reported %q, want the first reason %q", got, want)
	}
}
