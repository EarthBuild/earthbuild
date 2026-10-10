package cli

import "testing"

// One line of a step's live output, as the reader sees it.
//
// The prefix is not decoration - steps run concurrently, and an unattributed
// line is worse than none, because a reader debugs the wrong command. But a
// step that asked for raw output is writing for a parser rather than a reader,
// and a fold marker that is not at the start of its line is not a fold marker
// (E937).
func TestARawOutputLineCarriesNoPrefix(t *testing.T) {
	t.Parallel()

	if got, want := progressLine("Earthfile:7", "::group::x", true), "::group::x\n"; got != want {
		t.Errorf("a raw line is %q, want %q", got, want)
	}

	got := progressLine("Earthfile:7", "plain", false)
	if want := "  Earthfile:7    | plain\n"; got != want {
		t.Errorf("an ordinary line is %q, want %q", got, want)
	}
}
