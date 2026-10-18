//go:build darwin

package exec

import (
	"strings"
	"testing"
)

// TestWhatTheGuestSaysItFiledIsChecked.
//
// **The identity is derived by the guest and checked by the host**, and it
// crosses as text through a pipe. Anything that is not an identity and a size
// is a guest that did not do what was asked - a truncated write, a warning on
// the wrong stream - and reading it loosely would file a layer under a name
// nobody derived, which is the one thing the fleet never does (I6, §5.3).
func TestWhatTheGuestSaysItFiledIsChecked(t *testing.T) {
	t.Parallel()

	good := strings.Repeat("ab", 32)

	id, n, err := parseTaken(good + " 4096\n")
	if err != nil {
		t.Fatalf("a well-formed answer was refused: %v", err)
	}

	if id.String() != good {
		t.Errorf("filed as %v, want %s", id, good)
	}

	if n != 4096 {
		t.Errorf("filed %d bytes, want 4096", n)
	}

	for _, said := range []string{
		"",
		good,
		good + " not-a-number",
		"not-an-identity 4096",
		"  ",
	} {
		if _, _, err := parseTaken(said); err == nil {
			t.Errorf("%q was read as an element the guest filed", said)
		}
	}
}
