package exec

import (
	"fmt"
	"os"
	"time"
)

// staleGuestNote says the guest is older than the engine dialling it, or
// nothing.
//
// **The guest is a separate binary.** `go run ./cmd/earth-native` rebuilds the
// engine and not the agent, so a change under `engine/guest` is not in the guest
// that runs until somebody rebuilds it by hand - and the protocol version is the
// same on both sides, so the version check passes and nothing is said.
//
// That cost an increment: two guest-side fixes were measured against a guest
// built before them, the measurements showed them doing nothing, and what was
// written down was that a third bug existed. There was none (E498, E499).
//
// A note rather than a refusal. A released install ships both together with
// whatever timestamps the packaging gave them, and refusing to build over a file
// date would be refusing the common case to catch an uncommon one - the same
// call the case-insensitivity note makes (E26), and printed the same way E491
// left it: where a reader can act on it.
//
// A margin, because "older" by a second is two files written by one `go build`
// in the order the linker finished them.
func staleGuestNote(engine, guest string) string {
	const margin = time.Minute

	e, err := os.Stat(engine)
	if err != nil {
		return ""
	}

	g, err := os.Stat(guest)
	if err != nil {
		return ""
	}

	behind := e.ModTime().Sub(g.ModTime())
	if behind < margin {
		return ""
	}

	return fmt.Sprintf(
		"note: %s is %s older than this engine\n"+
			"  the guest is a separate binary and is not rebuilt with it, so a"+
			" change to the\n"+
			"  agent is not in the one that runs until it is rebuilt\n"+
			"  rebuild it: %sgo build -o %s ./cmd/earth-guestd\n",
		guest, behind.Round(time.Minute), crossPrefix(), guest)
}
