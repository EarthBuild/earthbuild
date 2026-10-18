//go:build darwin

package exec

import (
	"strings"
	"testing"
)

// This backend refuses `--isolate` rather than pretending.
//
// **Found by writing the specification** (§3.4b, I14): an engine that cannot
// provide the daemon provenance a step asked for refuses the step and does not
// substitute another. This backend was substituting.
//
// The sandbox VM's daemon is destroyed when the build ends, so it holds nothing
// an *earlier build* left - which is why a bare block here is safe. It is not
// destroyed between blocks of the same build, so it does hold whatever an
// earlier block of this build loaded into it. A block asking for `--isolate` and
// given that daemon is cached - the interpreter marks isolated blocks cacheable
// (E381) - under a key claiming an empty daemon, against an execution that saw
// somebody else's images.
//
// One build with two blocks is enough to reach it, which is not an exotic
// Earthfile.
func TestThisBackendRefusesIsolateRatherThanPretending(t *testing.T) {
	t.Parallel()

	_, err := dockerFor(true, "", "")
	if err == nil {
		t.Fatal("a block asking for a daemon of its own was quietly given the" +
			" build's shared one, and will be cached as though it had been empty")
	}

	for _, want := range []string{"--isolate", "native"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}

	// And a block that asked for nothing still works: the refusal is about the
	// promise this backend cannot keep, not about WITH DOCKER.
	_, err = dockerFor(false, "", "")
	if err != nil {
		t.Errorf("an ordinary block was refused too: %v", err)
	}
}
