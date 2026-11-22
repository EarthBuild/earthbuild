package cli

import (
	"os"
	"strings"
	"testing"
)

// TestPinningAnEarthfileAsksTheOrigin.
//
// **A build may be stale; a file committed to a repository may not.** A build
// reusing a remembered digest costs at worst a cache key coarser than it could
// be, and EARTH_PIN_TTL bounds how long (E703). `--pin` writes the digest into
// the user's Earthfile, where it stays until somebody edits it - so the same
// staleness there is a wrong file in a repository rather than a slow build.
//
// It asks the origin today because it calls `image.Resolve` directly. That is
// incidental, and the obvious tidy-up - routing both through the one cached
// resolver, for consistency - would be silent and wrong. So it is held here:
// read as a rule rather than inferred from the shape of the code.
func TestPinningAnEarthfileAsksTheOrigin(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("pin.go")
	if err != nil {
		t.Fatal(err)
	}

	for _, forbidden := range []string{"NewPins", "PinTTLFromEnv"} {
		if strings.Contains(string(b), forbidden) {
			t.Errorf("pin.go uses %s\n"+
				"  --pin writes the digest into the Earthfile, where it stays."+
				" A remembered\n"+
				"  answer there is a stale digest committed to a repository, not"+
				" a slow build,\n"+
				"  so this path asks the registry every time.", forbidden)
		}
	}
}
