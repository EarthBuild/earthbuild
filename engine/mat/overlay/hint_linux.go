package overlay

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// overlayfsMagic identifies overlayfs in a statfs result. Defined here because
// x/sys/unix does not export it.
const overlayfsMagic = 0x794c7630

// onOverlay reports whether a path is itself on an overlayfs.
//
// The one fact behind both the hint and the classification: overlayfs refuses
// to stack on overlayfs, which is the state of almost any container's root.
func onOverlay(path string) bool {
	var st unix.Statfs_t

	if unix.Statfs(path, &st) != nil {
		return false
	}

	return int64(st.Type) == overlayfsMagic //nolint:unconvert // this field is not this width on every platform
}

// unavailable reports that a mount failure is the machine's rather than the
// build's.
//
// Two ways for a machine to be unable to mount at all, and both are properties
// of where the engine is running: no CAP_SYS_ADMIN (E13), and a working
// directory that is itself on overlayfs, which is where every container puts
// you. Neither is a defect in this engine and neither can be fixed by retrying.
//
// Typed rather than matched on the message, because a caller deciding whether
// to skip a test or degrade a build must not be reading prose - and the prose
// is written for a person, so it changes.
func unavailable(err error, base string) bool {
	if errors.Is(err, unix.EPERM) {
		return true
	}

	return errors.Is(err, unix.EINVAL) && onOverlay(base)
}

// mountHint turns overlayfs's EINVAL into a sentence someone can act on.
//
// The kernel reports a bare "invalid argument" for every rejected mount, which
// is the least useful diagnostic it could produce. The overwhelmingly common
// cause is stacking: overlayfs refuses a lowerdir or upperdir that is itself on
// overlayfs, so running the engine inside a container whose root is overlay -
// which is to say, inside almost any container - fails here and says nothing
// about why.
//
// Returns the empty string when there is nothing useful to add; the caller
// appends it unconditionally.
func mountHint(err error, base string) string {
	if !errors.Is(err, unix.EINVAL) {
		return ""
	}

	if !onOverlay(base) {
		return ""
	}

	return fmt.Sprintf(
		"\n  %s is itself on overlayfs, and overlayfs cannot stack on overlayfs"+
			"\n  this is what happens when the engine runs inside a container whose root is overlay"+
			"\n  put the engine's working directory on a real filesystem: mount a volume"+
			"\n  (docker run -v /var/lib/earthbuild:/var/lib/earthbuild) or a tmpfs",
		base)
}
