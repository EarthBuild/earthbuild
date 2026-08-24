//go:build unix

package guest

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

// copyXattrs carries every extended attribute from src to dst.
//
// **Every one**, not a list. The previous version carried exactly two names,
// both the overlay's opaque marker, because that was what the bug in front of
// it needed - and `layer.Take` reads and hashes all of them, so a copy carrying
// two produced a layer that disagreed with its own digest about the rest.
//
// `security.capability` is the one that costs something visible. A binary given
// `cap_net_bind_service` by `setcap` carries the grant in that attribute, and a
// copy that drops it produces an image whose service cannot bind its port -
// at runtime, in a container, from a build that reported success. Ownership and
// mode are carried carefully a few lines away; this is the third thing a POSIX
// file's authority rests on.
//
// A name that cannot be set is an error rather than a silent omission, which is
// the rule the whiteouts arrived at (E88): an entry missing from a layer is a
// step's work discarded, and the only honest alternative to carrying it is
// saying so.
func copyXattrs(src, dst string) error {
	names, err := listXattrs(src)
	if err != nil {
		return err
	}

	for _, name := range names {
		if !ours(name) {
			continue
		}

		value, err := getXattr(src, name)
		if err != nil {
			// Gone between the list and the read, which a live filesystem may
			// do and which costs nothing to tolerate: there is no attribute to
			// lose.
			continue
		}

		err = unix.Lsetxattr(dst, name, value, 0)
		if err == nil {
			continue
		}

		// The overlay's opaque marker has a portable spelling, for the same
		// reason a whiteout does: a store the host filesystem owns will not
		// take a `trusted.` attribute, and a deletion must not be lost over
		// where it is being written (E94).
		if isOpaque(name) {
			err = writeOpaque(dst)
			if err != nil {
				return err
			}

			continue
		}

		return fmt.Errorf("carry the extended attribute %s onto %s: %w", name, dst, err)
	}

	return nil
}

// ours reports whether an attribute belongs to the layer rather than to the
// machine the file happens to be sitting on.
//
// `com.apple.provenance` is the case that forced this: macOS attaches it to
// files as its own bookkeeping, the build context is full of them, and the
// destination inside the sandbox will not take one - so refusing on an
// attribute the build never created failed every build that copies from the
// context on a Mac. The differential oracle caught it within a minute of the
// change.
//
// A namespace rule and not a name list, which is the distinction this whole run
// of experiments is about: `com.apple.` is one operating system's private
// bookkeeping about files it stores, and no layer this engine produces contains
// one. Everything else - user, trusted, security, system - describes the file
// and is carried or the copy fails.
func ours(name string) bool {
	return !strings.HasPrefix(name, "com.apple.")
}

func listXattrs(p string) ([]string, error) {
	size, err := unix.Llistxattr(p, nil)
	if err != nil || size == 0 {
		// Unsupported or none. A filesystem without them is not an error: the
		// source has no attributes, so the copy loses none.
		return nil, nil //nolint:nilerr // see above
	}

	buf := make([]byte, size)

	size, err = unix.Llistxattr(p, buf)
	if err != nil {
		return nil, nil //nolint:nilerr // as above
	}

	var out []string

	for name := range strings.SplitSeq(string(buf[:size]), "\x00") {
		if name != "" {
			out = append(out, name)
		}
	}

	return out, nil
}

func getXattr(p, name string) ([]byte, error) {
	size, err := unix.Lgetxattr(p, name, nil)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller decides what an unreadable one means
	}

	buf := make([]byte, size)

	size, err = unix.Lgetxattr(p, name, buf)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}

	return buf[:size], nil
}

// isOpaque reports whether an attribute is overlayfs's "this directory replaces
// the one below it" marker, under either namespace it uses.
func isOpaque(name string) bool {
	return name == "trusted.overlay.opaque" || name == "user.overlay.opaque"
}
