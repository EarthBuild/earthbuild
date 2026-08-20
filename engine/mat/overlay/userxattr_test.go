//go:build linux

package overlay

import (
	"strings"
	"testing"
)

// The opaque marker written is the one the mount will read.
//
// overlayfs keeps its own metadata in extended attributes, and which *namespace*
// they live in is a mount option:
//
//	default      trusted.overlay.*   needs CAP_SYS_ADMIN in the initial namespace
//	userxattr    user.overlay.*      writable by anyone who owns the file
//
// These are two halves of one decision and they are held in two files - the
// option string in `mountOptions`, the attribute name in the whiteout
// translator. Nothing made them agree.
//
// **What it costs when they disagree.** `user.overlay.opaque` under a default
// mount is an attribute the kernel ignores, so a directory that should replace
// the one below it merges with it instead: deleted files reappear. No error, no
// failed mount - a build that succeeds and produces the wrong filesystem, which
// is the worst failure this engine has (I2, I10).
//
// So the pairing is asserted rather than remembered.
func TestTheOpaqueMarkerMatchesTheMount(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		user bool
		want string
	}{
		{"a privileged mount uses the trusted namespace", false, "trusted.overlay.opaque"},
		{"an unprivileged mount uses the user namespace", true, "user.overlay.opaque"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := opaqueXattr(tc.user); got != tc.want {
				t.Errorf("the marker is %q and the mount would read %q", got, tc.want)
			}

			opts := mountOptions([]string{"/l"}, "/u", "/w", tc.user)

			if strings.Contains(opts, "userxattr") != tc.user {
				t.Errorf("the mount options do not say userxattr=%v: %s", tc.user, opts)
			}
		})
	}
}

// An unprivileged mount asks for userxattr, and a privileged one does not.
//
// Not cosmetic. Without it, an unprivileged overlay cannot rename a directory
// that came from a lower layer - it tries to record a redirect in
// `trusted.overlay.redirect`, cannot write there, and returns EIO. Measured on
// 6.12.90 in a user namespace:
//
//	opts=[]           -> mv: cannot remove 'm/d': Input/output error
//	opts=[,userxattr] -> RENAME-OK
//
// Which is `dpkg` unpacking any package that owns a directory:
//
//	unable to install new version of './usr/share/doc/unzip': Invalid cross-device link
//
// `apt-get install` is the most common line in an Earthfile, so this is not a
// corner. E103 recorded the limitation as kernel policy needing a maintainer's
// judgement; that was right about `redirect_dir=on`, which an unprivileged mount
// is refused outright, and wrong about there being no remedy.
func TestAnUnprivilegedMountAsksForUserXattr(t *testing.T) {
	t.Parallel()

	privileged := mountOptions([]string{"/l"}, "/u", "/w", false)
	if strings.Contains(privileged, "userxattr") {
		t.Errorf("a privileged mount does not need userxattr: %s", privileged)
	}

	unprivileged := mountOptions([]string{"/l"}, "/u", "/w", true)
	if !strings.Contains(unprivileged, "userxattr") {
		t.Errorf("an unprivileged mount cannot rename a lower directory without"+
			" userxattr, and gets EIO: %s", unprivileged)
	}
}
