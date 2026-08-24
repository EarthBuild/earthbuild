package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The socket lands where the step's client will look, following the image's own
// symlink.
//
// **`/var/run` is a symlink to `../run` in every Alpine-derived image**, which
// includes the official docker client images. A bind placed at
// `<root>/var/run/docker.sock` without resolving it is placed *through* that
// link: `<root>/var/run` resolves on the guest to `<root>/../run`, which is
// outside the step entirely - so the step finds nothing at the path it looks in,
// and the engine has written somewhere it had no business writing (E397).
//
// Resolved the way the step would resolve it: `resolveLast` reads the link's
// text against the step's root and clamps a climb, which is what the kernel does
// above a chroot.
func TestTheSocketFollowsTheImagesOwnSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "run"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.MkdirAll(filepath.Join(root, "var"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	// Exactly what the image ships.
	err = os.Symlink("../run", filepath.Join(root, "var", "run"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := socketTargetIn(root, filepath.Join(root, "var/run/docker.sock"))
	if err != nil {
		t.Fatalf("%v", err)
	}

	if want := filepath.Join(root, "run", "docker.sock"); got != want {
		t.Errorf("the socket would be bound at %s, want %s", got, want)
	}

	if !strings.HasPrefix(got, root+string(filepath.Separator)) {
		t.Errorf("the socket would be bound outside the step: %s", got)
	}
}

// A root with no /var/run at all still gets one.
//
// A scratch image has nothing, and the daemon's socket has to appear somewhere:
// the directory is made rather than the step refused.
func TestASocketPathThatDoesNotExistIsMade(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	got, err := socketTargetIn(root, filepath.Join(root, "var/run/docker.sock"))
	if err != nil {
		t.Fatalf("%v", err)
	}

	_, err = os.Stat(filepath.Dir(got))
	if err != nil {
		t.Errorf("the directory the socket appears in was not made: %v", err)
	}
}

// A link that climbs out is clamped rather than followed.
//
// An image is an input and §5.3 does not trust one: a `/var/run -> ../../../etc`
// planted in a base image would otherwise have the engine bind a live docker
// socket into the guest's own filesystem.
func TestALinkThatClimbsOutIsClamped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "var"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink("../../../../etc", filepath.Join(root, "var", "run"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := socketTargetIn(root, filepath.Join(root, "var/run/docker.sock"))
	if err != nil {
		return // refusing is also an answer
	}

	if !strings.HasPrefix(got, root+string(filepath.Separator)) {
		t.Errorf("a base image chose where this engine binds a docker socket: %s", got)
	}
}
