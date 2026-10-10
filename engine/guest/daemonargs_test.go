package guest

import (
	"strings"
	"testing"
)

// Every default a daemon has that a step cannot use is overridden.
//
// **Each of these was a failure to start**, in the order the daemon reported
// them, on a real machine (E364). A test that only checked the list would say
// nothing about why; what it checks is that no flag is the *host's* default,
// which is the property every one of them shares and the one a later edit would
// break by tidying.
func TestNoDaemonPathIsTheHostDefault(t *testing.T) {
	t.Parallel()

	got := strings.Join(daemonArgs("/store/docker-cache/layers", "/run/d.sock", false), " ")

	for _, host := range []string{
		"/var/run/docker.pid", "/var/lib/docker", "/run/docker",
	} {
		if strings.Contains(got, host) {
			t.Errorf("a step's daemon would use the host's %s:\n%s", host, got)
		}
	}

	for _, want := range []string{
		"--group=", "--pidfile=", "--data-root=", "--exec-root=", "--host=unix://",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %s, and its default is the host's:\n%s", want, got)
		}
	}
}

// The storage driver is one that works without asking the kernel for anything.
//
// A step's filesystem is already an overlay, and overlay-on-overlay needs
// support it cannot assume. `vfs` copies where overlay links, which is slower
// and works everywhere - the right trade for the first daemon this engine runs,
// and a decision worth being able to find when somebody measures it.
func TestTheStorageDriverAsksTheKernelForNothing(t *testing.T) {
	t.Parallel()

	got := strings.Join(daemonArgs("/root", "/sock", false), " ")

	if !strings.Contains(got, "--storage-driver=vfs") {
		t.Errorf("the daemon picks its own storage driver:\n%s", got)
	}
}

// The guest is not told which cache a step was given.
//
// Separation used to be asserted here, as two names producing two data roots -
// and that test went stale the moment the daemon moved into the step: the root
// is a constant inside the step's filesystem, and which storage is behind it is
// decided by what the executor mounts there (E365). Two caches are two mounts,
// not two command lines.
//
// So the assertion is the opposite one, and it is the load-bearing half: nothing
// in the daemon's arguments varies with the cache, because a guest that knew the
// cache name would be a second place the rule is written.
func TestTheGuestIsNotToldWhichCacheAStepWasGiven(t *testing.T) {
	t.Parallel()

	got := strings.Join(daemonArgs("/var/lib/earthbuild-docker", "/var/run/docker.sock", false), " ")

	// Distinctive names, not "a" and "b": the first version of this test looked
	// for those as substrings and found the "b" in `--bridge`, failing against
	// correct code. *An assertion that matches by accident* is as useless as one
	// that never runs, and cheaper to write.
	for _, name := range []string{"layers", "docker-cache", "buildcache"} {
		if strings.Contains(got, name) {
			t.Errorf("the daemon's arguments name the cache %q:\n%s", name, got)
		}
	}
}

// The daemon's sockets fit in a sockaddr.
//
// `sun_path` is 108 bytes on Linux and containerd refuses anything over 104. A
// step's root is a long path - a store, a handle, a root - and
// `<step>/var/lib/earthbuild-docker/exec/containerd/containerd-debug.sock`
// exceeded it on the first real run, so the daemon started, reached containerd,
// and timed out waiting for something that could not bind (E375).
//
// The exec root is therefore *not* under the step: it holds runtime sockets
// rather than storage, it is thrown away with the daemon, and the shim has
// already mounted a private tmpfs at /run that no other daemon can see. Short,
// private, and gone when the namespace is.
func TestTheDaemonsSocketsFitInASockaddr(t *testing.T) {
	t.Parallel()

	// About as long as a real one gets: a store, a handle, and a root.
	step := "/var/lib/earthbuild/store/handles/" + strings.Repeat("h", 40) + "/root"

	for _, a := range daemonArgs(step+"/var/lib/earthbuild-docker", step+"/var/run/docker.sock", false) {
		if !strings.HasPrefix(a, "--exec-root=") {
			continue
		}

		// containerd puts its own socket two directories under this one.
		const deepest = "/containerd/containerd-debug.sock"

		if got := len(strings.TrimPrefix(a, "--exec-root=") + deepest); got > 104 {
			t.Errorf("a socket under the exec root is %d bytes, and 104 is the"+
				" limit:\n  %s%s", got, strings.TrimPrefix(a, "--exec-root="), deepest)
		}
	}
}
