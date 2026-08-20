package exec

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// A step that inherits a daemon is given the socket to reach it.
//
// Without this the whole default does nothing: `dockerPlanFor` decides to share,
// the step is told it is sharing, and nothing is mounted - so its client finds no
// socket and reports a daemon that is not running, for a daemon that is. The
// decision and its consequence are separate code, and a decision whose
// consequence is missing looks exactly like the feature not being requested.
func TestAnInheritingStepIsGivenTheSocket(t *testing.T) {
	t.Parallel()

	got := withSocket(dockerPlan{Inherit: true}, nil)

	var found bool

	for _, m := range got.Mounts {
		if m.Sandbox == hostDockerSocket && m.Target == hostDockerSocket {
			found = true
		}
	}

	if !found {
		t.Errorf("an inheriting step was given no socket to inherit through: %v",
			got.Mounts)
	}
}

// A step with a daemon of its own is not given anybody else's socket.
//
// Its daemon binds the socket inside the step's own filesystem (E370), so a
// mount here would put a second one at the same path - and which of the two the
// client reached would depend on the order the guest applied them. Isolation
// that depends on mount ordering is not isolation.
func TestAStepWithItsOwnDaemonIsGivenNoOtherSocket(t *testing.T) {
	t.Parallel()

	got := withSocket(dockerPlan{Own: true}, nil)

	for _, m := range got.Mounts {
		if m.Target == hostDockerSocket {
			t.Errorf("a step with its own daemon was also given somebody else's"+
				" socket at the same path: %+v", m)
		}
	}
}

// The client is offered either way, and its absence is not fatal.
//
// The daemon is what no image can supply; the client is a convenience an image
// often carries itself - alpine packages `docker-cli`. Refusing a build for want
// of a client on the machine declines a feature that would have worked (E145).
func TestTheClientIsOfferedAndItsAbsenceIsNotFatal(t *testing.T) {
	t.Parallel()

	with := withSocket(dockerPlan{Own: true}, []guest.Mount{
		{Sandbox: "/usr/bin/docker", Target: "/usr/bin/docker", ReadOnly: true},
	})

	if len(with.Mounts) != 1 {
		t.Errorf("the client was not passed on: %v", with.Mounts)
	}

	if got := withSocket(dockerPlan{Own: true}, nil); len(got.Mounts) != 0 {
		t.Errorf("a machine with no client produced mounts anyway: %v", got.Mounts)
	}
}
