package guest

import (
	"path/filepath"
	"strings"
	"testing"
)

// The daemon's paths are the step's paths, resolved against the step's root.
//
// It runs beside the step rather than in it: the guest's own `dockerd`, writing
// into the step's filesystem at the guest paths for what the step calls
// `/var/lib/earthbuild-docker` and `/var/run/docker.sock`. That is deliberate -
// the image then needs a Docker *client* and not a daemon, which is what images
// using WITH DOCKER actually tend to have.
func TestADaemonsPathsAreResolvedAgainstTheStepsRoot(t *testing.T) {
	t.Parallel()

	root, sock := daemonPaths("/steps/h1",
		&Daemon{Root: "/var/lib/earthbuild-docker", Socket: "/var/run/docker.sock"})

	if root != filepath.Join("/steps/h1", "var/lib/earthbuild-docker") {
		t.Errorf("the daemon's root is %q, which is not inside the step", root)
	}

	if sock != filepath.Join("/steps/h1", "var/run/docker.sock") {
		t.Errorf("the socket is %q, which is not inside the step", sock)
	}
}

// No request can put the daemon outside the step.
//
// §5.3: the host is not trusted by the guest, and these two strings are the only
// part of a daemon request that becomes a filesystem path. A `..` in either
// would otherwise have the guest write the daemon's storage - or a listening
// socket - somewhere on its own disk, outside every handle.
//
// Containment, not refusal. `/../../etc` inside a chroot *is* `/etc`, so
// normalising is what the step's own kernel would do with the same string, and
// refusing it would make this protocol's paths mean something different from
// every other path the step can name.
func TestNoDaemonRequestEscapesTheStep(t *testing.T) {
	t.Parallel()

	const step = "/steps/h1"

	for _, d := range []Daemon{
		{Root: "/../../etc", Socket: "/var/run/docker.sock"},
		{Root: "/var/lib/d", Socket: "/../../tmp/x.sock"},
		{Root: "/a/../../..", Socket: "/b/../../.."},
	} {
		root, sock := daemonPaths(step, &d)

		for _, got := range []string{root, sock} {
			if got != step && !strings.HasPrefix(got, step+string(filepath.Separator)) {
				t.Errorf("%+v put %q outside %s", d, got, step)
			}
		}
	}
}
