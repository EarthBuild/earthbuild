package exec

import (
	"strings"
	"testing"
)

// What a rootless daemon would need here, and what is missing.
//
// **`WITH DOCKER` refuses today with "a daemon of its own is not built yet"**
// (E355), which is true and tells an operator nothing about their machine. A
// rootless daemon needs three things that are properties of the host rather than
// of this engine: the setuid helpers that map a range of ids, a range allocated
// to this user, and a kernel that lets an unprivileged process make a user
// namespace.
//
// A machine with none of them cannot host one however much is built; a machine
// with two of them is one `usermod` away. Those are different sentences and the
// refusal should be able to say which (I10, E361).
func TestRootlessReadinessNamesWhatIsMissing(t *testing.T) {
	t.Parallel()

	none := rootlessReady(rootlessProbe{
		look:   func(string) (string, bool) { return "", false },
		subid:  func(string) (bool, error) { return false, nil },
		userns: func() (int, error) { return 0, nil },
	})

	if none.OK {
		t.Fatal("a machine with no helpers, no id range and no namespaces is" +
			" reported ready")
	}

	for _, want := range []string{"newuidmap", "/etc/subuid", "user namespace"} {
		if !strings.Contains(none.Why, want) {
			t.Errorf("the reason does not mention %q:\n%s", want, none.Why)
		}
	}

	// Two of three: the reason names the one that is missing and not the two
	// that are not, because an operator reads it to decide what to do next.
	partial := rootlessReady(rootlessProbe{
		look:   func(string) (string, bool) { return "/usr/bin/newuidmap", true },
		subid:  func(string) (bool, error) { return false, nil },
		userns: func() (int, error) { return 15000, nil },
	})

	if partial.OK {
		t.Fatal("a machine with no id range is reported ready")
	}

	if strings.Contains(partial.Why, "newuidmap") {
		t.Errorf("the reason blames a helper that is present:\n%s", partial.Why)
	}

	if !strings.Contains(partial.Why, "/etc/subuid") {
		t.Errorf("the reason does not name the missing piece:\n%s", partial.Why)
	}
}

// A machine with all three is ready, and says nothing.
//
// The other half: a readiness check that never reports ready would refuse the
// configuration this work exists to reach, and would do it with a message that
// reads like a diagnosis.
func TestAMachineWithAllThreeIsReady(t *testing.T) {
	t.Parallel()

	got := rootlessReady(rootlessProbe{
		look:   func(string) (string, bool) { return "/usr/bin/newuidmap", true },
		subid:  func(string) (bool, error) { return true, nil },
		userns: func() (int, error) { return 15000, nil },
	})

	if !got.OK {
		t.Fatalf("a machine with helpers, a range and namespaces is not ready:"+
			" %s", got.Why)
	}

	if got.Why != "" {
		t.Errorf("a ready machine explains itself: %q", got.Why)
	}
}

// A kernel that allows no user namespaces is not "zero configured", it is off.
//
// `user.max_user_namespaces = 0` is how a distribution disables the feature, and
// it is the one of the three an operator most often cannot change - so it is
// worth naming rather than folding into a general "cannot".
func TestAKernelWithNoUserNamespacesIsNamed(t *testing.T) {
	t.Parallel()

	got := rootlessReady(rootlessProbe{
		look:   func(string) (string, bool) { return "/usr/bin/newuidmap", true },
		subid:  func(string) (bool, error) { return true, nil },
		userns: func() (int, error) { return 0, nil },
	})

	if got.OK {
		t.Fatal("a kernel allowing no user namespaces is reported ready")
	}

	if !strings.Contains(got.Why, "user namespace") {
		t.Errorf("the reason does not name it:\n%s", got.Why)
	}
}

// A machine with everything except a daemon to run is not ready.
//
// **E361 asked Docker's question rather than this engine's.** Rootless docker
// ships a script that makes a user namespace with `rootlesskit` and a network
// with `slirp4netns`, and the readiness check was written from that list. This
// engine's guest already makes the namespace - that is what a step runs in - so
// those are not the prerequisites.
//
// What is: a `dockerd` this engine can give a step. The machine this project
// measures on has one and has neither of the others, and reported **ready**
// while having no way to give a step a daemon at all (E363).
func TestAMachineWithNoDaemonToRunIsNotReady(t *testing.T) {
	t.Parallel()

	got := rootlessReady(rootlessProbe{
		look: func(prog string) (string, bool) {
			// Everything the namespace needs, and no daemon.
			return "/usr/bin/" + prog, prog != "dockerd"
		},
		subid:  func(string) (bool, error) { return true, nil },
		userns: func() (int, error) { return 15000, nil },
	})

	if got.OK {
		t.Fatal("a machine with no dockerd is reported ready to run one")
	}

	if !strings.Contains(got.Why, "dockerd") {
		t.Errorf("the reason does not name it:\n%s", got.Why)
	}
}

// And what rootless docker needs is not what this engine needs.
//
// `rootlesskit` and `slirp4netns` are how Docker's own script makes a namespace
// and a network for a daemon started from a login shell. A step here is already
// inside a namespace this engine made, so asking for them would refuse machines
// that can host a daemon perfectly well - including the one this project
// measures on, which has neither (E363).
func TestRootlessDockersOwnToolsAreNotRequired(t *testing.T) {
	t.Parallel()

	got := rootlessReady(rootlessProbe{
		look: func(prog string) (string, bool) {
			switch prog {
			case "rootlesskit", "slirp4netns", "fuse-overlayfs":
				return "", false
			}

			return "/usr/bin/" + prog, true
		},
		subid:  func(string) (bool, error) { return true, nil },
		userns: func() (int, error) { return 15000, nil },
	})

	if !got.OK {
		t.Errorf("a machine without docker's own rootless tooling is refused:"+
			"\n%s", got.Why)
	}
}
