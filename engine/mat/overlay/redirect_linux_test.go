//go:build linux

package overlay

import (
	"strings"
	"testing"
)

// TestAPrivilegedOverlayAsksToRedirectDirectories.
//
// **The rename overlayfs refuses unless it is allowed to record where a
// directory went.** A directory that exists only in a lower layer cannot be
// renamed: overlayfs would have to leave a `redirect` attribute behind saying
// where it came from, the feature is off by default (`redirect_dir` reads `N`
// on the machines this runs on), and a mount that has not asked for it returns
// EIO instead.
//
// What that costs is not hypothetical. `cargo install --root $CARGO_HOME`
// renames directories inside its target, and the engine reports the step as
// `a path argument that could not be read: input/output error` - so it builds,
// and it is never observed, and the observed-input tier can never hit for it.
// `dpkg` does the same and reports `Invalid cross-device link` for any package
// owning a directory.
//
// Asked only where the metadata can actually be written. The attribute lives in
// `trusted.overlay.*`, which needs privilege in the initial user namespace; a
// rootless mount keeps its metadata in `user.overlay.*` instead and the kernel
// refuses `redirect_dir=on` to it outright, so asking there would turn a
// working degraded mount into no mount at all. The probe that chooses between
// the two namespaces is the same one that decides this.
func TestAPrivilegedOverlayAsksToRedirectDirectories(t *testing.T) {
	t.Parallel()

	privileged := mountOptions([]string{"/l"}, "/u", "/w", false)
	if !strings.Contains(privileged, "redirect_dir=on") {
		t.Errorf("a mount that can write trusted.overlay.* does not ask to"+
			" redirect directories, so renaming one out of a lower layer fails"+
			"\n  options: %s", privileged)
	}

	rootless := mountOptions([]string{"/l"}, "/u", "/w", true)
	if strings.Contains(rootless, "redirect_dir=on") {
		t.Errorf("a rootless mount asks for redirect_dir, which the kernel"+
			" refuses - so the mount fails and the step gets no filesystem at"+
			" all, rather than one that cannot rename a directory"+
			"\n  options: %s", rootless)
	}

	if !strings.Contains(rootless, "userxattr") {
		t.Errorf("a rootless mount lost its userxattr: %s", rootless)
	}
}
