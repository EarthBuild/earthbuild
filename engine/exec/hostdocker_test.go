package exec

import (
	"strings"
	"testing"
)

// WITH DOCKER on a sandbox that is this machine is a trust decision.
//
// The mounts a `WITH DOCKER` step gets are the client binary, the plugin
// directory and the daemon socket, taken from **the sandbox's own filesystem**.
// On macOS that filesystem belongs to a disposable virtual machine: the socket
// is the VM's daemon, and a step given root over it has root over a machine
// that is thrown away when the build ends.
//
// On the native backend the sandbox's filesystem *is this machine*. Measured on
// a 6.12 host:
//
//	docker binary      /home/…/.nix-profile/bin/docker   (not /usr/local/bin)
//	socket             srw-rw---- root docker            present, daemon 28.5.2
//	from inside the    docker info -> 28.5.2             reachable: supplementary
//	  user namespace                                     groups survive the map
//
// So `WITH DOCKER` on Linux was never the project it looked like - no nested
// rootless daemon, no cgroup delegation. It is a path lookup. The engine asked
// for `/usr/local/bin/docker` because that is where Apple's sandbox image puts
// it, found nothing, and reported that the machine had no docker (E116).
//
// **And that is the reason it must not simply be fixed.** Handing a build step
// the host's docker socket gives that step root on the developer's machine: it
// can mount `/` into a container and write anywhere. That is a different trust
// domain from the VM case (green paper A5), not a different path, so it is
// refused by default and the refusal says what it would cost.
func TestHostDockerIsRefusedUnlessAskedFor(t *testing.T) {
	t.Parallel()

	_, _, err := hostDockerMounts(
		func(string) (string, bool) { return buildProbeELF(t, true), true }, false)
	if err == nil {
		t.Fatal("the host's daemon was handed to a step without being asked for")
	}

	// The refusal is the whole interface here, so it is asserted rather than
	// assumed: what it would do, what it costs, and how to say yes.
	for _, want := range []string{"root on this machine", envAllowHostDocker} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

// Asked for, it uses the docker that is actually installed.
//
// Not `/usr/local/bin/docker`. That path is a property of the sandbox image the
// Apple backend boots, and hard-coding it is what made a machine with docker 28
// running report that it had none.
func TestHostDockerUsesTheInstalledClient(t *testing.T) {
	t.Parallel()

	// Somewhere that is not /usr/local/bin, which is the whole point, and a
	// real ELF because linkage is checked before the mount is offered.
	installed := buildProbeELF(t, true)

	got, _, err := hostDockerMounts(func(string) (string, bool) { return installed, true }, true)
	if err != nil {
		t.Fatalf("allowed and still refused: %v", err)
	}

	var client, socket bool

	for _, m := range got {
		switch m.Sandbox {
		case installed:
			client = true

			if m.Target != dockerClientPath {
				t.Errorf("the client lands at %s; a step's PATH is the image's,"+
					" so it has to appear where the image expects it", m.Target)
			}

			if !m.ReadOnly {
				t.Error("the client is mounted writable")
			}

		case dockerSocketPath:
			socket = true

			if m.ReadOnly {
				t.Error("the socket is read-only, so the client cannot talk to the daemon")
			}
		}
	}

	if !client {
		t.Error("the installed client was not mounted")
	}

	if !socket {
		t.Error("the daemon socket was not mounted")
	}
}

// A static client is accepted.
//
// The companion, because "refuse dynamic binaries" is satisfiable by refusing
// everything - and then the capability is unreachable on the machines where it
// does work.
func TestAStaticallyLinkedClientIsAccepted(t *testing.T) {
	t.Parallel()

	static := buildProbeELF(t, true)

	got, _, err := hostDockerMounts(func(string) (string, bool) { return static, true }, true)
	if err != nil {
		t.Fatalf("a static client was refused: %v", err)
	}

	if len(got) == 0 {
		t.Error("no mounts for a client that would run")
	}
}

// A client that cannot run is skipped, not fatal - the socket still goes in.
//
// E117 refused the whole build when the host's docker client was dynamically
// linked, on the grounds that mounting it produces `docker: not found` about a
// file that is demonstrably there. That is right about the *client* and too
// strong about the *step*: alpine packages `docker-cli`, so an image can carry
// its own, and the only thing it cannot supply is the socket.
//
// So the client is offered when it will run and omitted when it will not, and
// the socket goes in either way. A step whose image has a client works; one
// whose image has none fails with its own shell's message, which is the truth
// about that image rather than a confusing claim about the mount.
//
// This is the third of E117's three options - host client, shipped client,
// client from the image - and the only one that needs nothing new: no vendored
// binary to keep current, and no refusal on a machine where the feature would
// have worked.
func TestAnUnusableClientStillLeavesTheSocket(t *testing.T) {
	t.Parallel()

	dynamic := buildProbeELF(t, false)

	got, _, err := hostDockerMounts(func(string) (string, bool) { return dynamic, true }, true)
	if err != nil {
		t.Fatalf("a dynamically linked client refused the whole build: %v", err)
	}

	var client, socket bool

	for _, m := range got {
		if m.Sandbox == dynamic {
			client = true
		}

		if m.Sandbox == dockerSocketPath {
			socket = true
		}
	}

	if client {
		t.Error("a client that cannot run inside the step was mounted anyway," +
			" which is `docker: not found` about a file that is there")
	}

	if !socket {
		t.Error("the socket was withheld because the client was unusable," +
			" so an image carrying its own client cannot reach the daemon either")
	}
}

// And a machine with no client at all still gets the socket.
//
// The same reasoning one step further: the host's client is a convenience, and
// the daemon is the thing only the host can provide.
func TestNoHostClientStillLeavesTheSocket(t *testing.T) {
	t.Parallel()

	got, _, err := hostDockerMounts(func(string) (string, bool) { return "", false }, true)
	if err != nil {
		t.Fatalf("a machine with no docker client refused the whole build: %v", err)
	}

	if len(got) == 0 {
		t.Fatal("nothing was mounted, so a step cannot reach the daemon")
	}

	for _, m := range got {
		if m.Sandbox == dockerSocketPath {
			return
		}
	}

	t.Error("the socket was not mounted")
}
