//go:build darwin

package cli_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// requireSandbox skips unless this machine can run a step in a sandbox.
func requireSandbox(t *testing.T) {
	t.Helper()

	err := exec.NewApple().Available()
	if err != nil {
		t.Skipf("apple container backend unavailable: %v", err)
	}
}

// sandboxHostsDocker reports whether this backend can run a docker daemon
// inside a step's sandbox.
//
// A capability, not a platform. Apple's backend boots a VM from a sandbox image
// that carries dockerd, so `WITH DOCKER` has somewhere to run. The native
// backend gives a step its own layer stack and the host's namespaces, and
// nothing puts a daemon in it - the engine already refuses clearly:
//
//	the sandbox has no /usr/local/bin/docker to give this step
//	a WITH DOCKER block needs a sandbox image with a daemon in it
//
// Which is I11 working as intended. It is a real gap in S4 on Linux and it is
// recorded as one; what it is not is a reason to build the test for darwin only,
// because then nobody finds out when it closes.
func sandboxHostsDocker() bool { return true }
