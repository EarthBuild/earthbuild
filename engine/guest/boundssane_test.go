package guest

import (
	"testing"
	"time"
)

// The waits a guest imposes on itself are finite and of a plausible size.
//
// **This is the guard E442 actually needs.** That defect was not a bound of the
// wrong length, it was no bound at all: `Release` used `context.Background()`
// and `Dial` read the handshake with nothing to stop it, so a guest that went
// quiet stopped the build for ever - in a deferred call during teardown, where
// nothing was left to interrupt it.
//
// The tests that watch those waits give up now supply their own bound, because
// waiting out sixty real seconds to watch a timeout fire cost this package more
// than every other test in it put together. That makes them fast and leaves the
// production numbers unwatched, which is what this restores: a bound that had
// been removed, or set to something no one will sit through, fails here in no
// time at all rather than in the ninety seconds it would take to observe.
//
// The ceiling is deliberately loose. What is being refused is a number that is
// not a wait but a hang; picking the right length is a judgement recorded in
// each constant's own comment, and not something to pin twice.
func TestTheGuestsOwnWaitsAreBounded(t *testing.T) {
	t.Parallel()

	const noOneWaitsThatLong = 5 * time.Minute

	for name, got := range map[string]time.Duration{
		"waitAtMost":     waitAtMost,
		"greetingAtMost": greetingAtMost,
		"releaseAtMost":  releaseAtMost,
	} {
		if got <= 0 {
			t.Errorf("%s is %v, so nothing bounds that wait and a guest that"+
				" stops answering stops the build (E442)", name, got)
		}

		if got > noOneWaitsThatLong {
			t.Errorf("%s is %v, which is not a wait anybody sits through - a"+
				" bound that long is the hang it was meant to prevent (E442)",
				name, got)
		}
	}
}
