package exec

import "testing"

// Prewarm and Start both call ensureRunning, deliberately: the boot is
// overlapped with planning because it needs nothing the Earthfile says (E537).
// When the VM is already up that makes the whole check run twice - two container
// listings and two store checks, about 55ms of subprocess work for an answer
// already known (E873).
//
// Remembering it is only safe while the VM is still there, so the memo has to be
// cleared by the two things that take it away. The dial-failure path in
// Executor.client stops, removes, and starts again; a memo that survived that
// would skip the reboot and the retry would connect to nothing.
func TestTheBootMemoIsClearedByWhateverTakesTheVMAway(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		take func(a *Apple)
	}{
		{"Stop", func(a *Apple) { _ = a.Stop() }},
		{"Remove", func(a *Apple) { _ = a.Remove() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			a := &Apple{}
			a.markBooted()

			if !a.alreadyBooted() {
				t.Fatal("markBooted did not take")
			}

			c.take(a)

			if a.alreadyBooted() {
				t.Errorf("%s left the boot memo set: a later Start would skip the reboot", c.name)
			}
		})
	}
}
