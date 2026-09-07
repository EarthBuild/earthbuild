//go:build linux

package cli_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// requireSandbox skips unless this machine can run a step in a sandbox.
func requireSandbox(t *testing.T) {
	t.Helper()

	// The guest first, and this is why it is here rather than in each caller.
	// `Available` looks for `earth-guestd`, so asking it before one exists skips
	// with `cannot find earth-guestd` on every machine that builds this from
	// source - which is every machine. Three tests had the order the other way
	// round, including the flagship end-to-end check for Κ₂, and all three
	// silently did not run (E218).
	t.Setenv("EARTH_GUESTD", buildGuestd(t))

	err := exec.NewNative().Available()
	if err != nil {
		t.Skipf("native backend unavailable: %v", err)
	}
}

// False, and the reason has narrowed. The native backend *can* host a WITH
// DOCKER step now: the host lends its daemon socket and the step's image
// supplies the client (E145). Two things still stand between that and the
// corpus - it is opt-in, because handing a step this machine's daemon is root
// on this machine, and the corpus Earthfiles expect the engine to provide
// `docker` rather than carrying `docker-cli` themselves.
//
// So this stays false and the tests that need it keep skipping, but for a
// smaller reason than "this backend cannot".
func sandboxHostsDocker() bool { return false }
