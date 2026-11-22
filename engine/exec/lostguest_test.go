//go:build linux

package exec

import (
	"errors"
	"strings"
	"testing"
)

// A guest that stops mid-build is quoted, like one that never answered.
//
// **The console is where a guest's last words are, whenever it stops.** A
// failure to connect already quotes it; a connection *lost* part-way through a
// build did not, though it is the same guest, the same console and the same
// question - what happened in there.
//
// Without it the engine reports "guest connection lost: unmarshal: unexpected
// end of JSON input", which says only that the far end stopped writing. A panic
// in the agent, an OOM kill, a kernel oops: all three produce that sentence and
// all three say so on the console.
func TestAGuestThatStopsIsQuoted(t *testing.T) {
	t.Parallel()

	sb := consoleSandbox{tail: "\n  the guest said: panic: runtime error"}

	err := errors.New("guest connection lost: unmarshal: unexpected end of JSON input")

	got := lostGuest(err, sb)
	if !strings.Contains(got.Error(), "panic: runtime error") {
		t.Errorf("a guest that stopped was not quoted: %s", got)
	}

	if !errors.Is(got, err) {
		t.Error("the wrapped error is no longer matchable with errors.Is")
	}
}

// Anything that is not a lost connection is left alone.
//
// A console tail on a step that merely exited non-zero is noise: the guest is
// fine, and the last thing it printed has nothing to do with the failure.
func TestAnOrdinaryFailureIsNotGivenAConsole(t *testing.T) {
	t.Parallel()

	sb := consoleSandbox{tail: "\n  the guest said: something"}

	for _, msg := range []string{
		"exit status 1",
		"no space left on device",
		"the guest did not answer the handshake within 30s",
	} {
		err := errors.New(msg)
		if got := lostGuest(err, sb); got.Error() != msg {
			t.Errorf("%q was given a console it did not need: %s", msg, got)
		}
	}
}
