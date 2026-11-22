//go:build linux && integration

package exec

import (
	"testing"
)

// A build running inside a container with a daemon shares that daemon.
//
// The inception decision, asserted where it actually applies. Everything else
// about it is unit-tested with the three inputs supplied by hand (E380, E383);
// this runs in a real container and lets the machine answer them, which is the
// only way to find out whether `/.dockerenv` and a socket are where this engine
// thinks they are.
//
// Skipped rather than failed on a machine: outside a container there is nothing
// to inherit, and that is the case every other test already covers.
func TestABuildInsideAContainerSharesItsDaemon(t *testing.T) {
	if !hereInContainer() {
		t.Skip("not running inside a container, so there is no outer daemon to share")
	}

	if !statSocket(hostDockerSocket) {
		t.Skipf("no socket at %s: run this container with -v %s:%s",
			hostDockerSocket, hostDockerSocket, hostDockerSocket)
	}

	plan, err := dockerFor(false, "", "")
	if err != nil {
		t.Fatalf("a bare block inside a container was refused: %v", err)
	}

	if !plan.Inherit {
		t.Error("a build inside a container with a daemon started one of its own;" +
			" the outer step's is what an author wants, and starting a second is" +
			" a daemon inside a daemon nobody asked for")
	}

	if plan.Own {
		t.Error("the step was given a daemon of its own as well as an inherited one")
	}

	// The socket has to travel, or the decision means nothing (E385).
	var carried bool

	for _, m := range plan.Mounts {
		if m.Sandbox == hostDockerSocket && m.Target == hostDockerSocket {
			carried = true
		}
	}

	if !carried {
		t.Errorf("the step inherits a daemon and is given no socket to reach it"+
			" through: %v", plan.Mounts)
	}
}

// And `--isolate` inside a container still gets its own.
//
// The flag's whole purpose is the nesting case: a build testing this engine's
// caching runs inside a container, and sharing the outer daemon is exactly the
// thing it must not do.
func TestIsolateInsideAContainerStillGetsItsOwn(t *testing.T) {
	if !hereInContainer() {
		t.Skip("not running inside a container")
	}

	plan, err := dockerFor(true, "", "")
	if err != nil {
		t.Fatalf("--isolate was refused inside a container: %v", err)
	}

	if !plan.Own || plan.Inherit {
		t.Errorf("--isolate inside a container did not get a daemon of its own:"+
			" own=%v inherit=%v", plan.Own, plan.Inherit)
	}
}
