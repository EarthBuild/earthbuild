//go:build unix

package layer

import (
	"io/fs"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// platformMeta fills in the fields only a syscall can answer: ownership, device
// numbers and hardlink identity.
//
// Without these a layer captured as root and one captured as a user look
// identical, and restoring the second over the first silently changes who owns
// every file in the image.
func platformMeta(e *entry, info fs.FileInfo, inodes map[uint64]string) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}

	e.uid, e.gid = observedOwner(st.Uid, st.Gid)
	e.rdev = uint64(st.Rdev) //nolint:unconvert // this field is not this width on every platform

	if st.Nlink > 1 && info.Mode().IsRegular() {
		ino := uint64(st.Ino) //nolint:unconvert // widths differ across platforms
		if first, seen := inodes[ino]; seen {
			e.hardlink = first
		} else {
			inodes[ino] = e.path
		}
	}
}

// readXattrs returns the extended attributes of a path, sorted by name so that
// the order the filesystem happens to return them cannot reach the digest.
func readXattrs(p string) ([]xattr, error) {
	size, err := unix.Llistxattr(p, nil)
	if err != nil || size == 0 {
		return nil, err //nolint:wrapcheck // the caller ignores it; xattrs are optional
	}

	buf := make([]byte, size)
	size, err = unix.Llistxattr(p, buf)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}

	var out []xattr

	for _, name := range splitNul(buf[:size]) {
		if assembledBy(name) {
			continue
		}

		vsize, err := unix.Lgetxattr(p, name, nil)
		if err != nil {
			continue
		}

		v := make([]byte, vsize)
		vsize, err = unix.Lgetxattr(p, name, v)
		if err != nil {
			continue
		}

		out = append(out, xattr{name: name, value: string(v[:vsize])})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })

	return out, nil
}

func splitNul(b []byte) []string {
	var (
		out   []string
		start int
	)

	for i, c := range b {
		if c == 0 {
			if i > start {
				out = append(out, string(b[start:i]))
			}

			start = i + 1
		}
	}

	return out
}

// assembledBy reports whether an attribute describes how a filesystem was put
// together rather than what is at a path.
//
// overlayfs keeps its bookkeeping in extended attributes and writes it onto the
// *upper* layer - `user.overlay.origin` records which lower inode an entry was
// copied up from, under `userxattr`; `trusted.overlay.*` is the same thing on a
// privileged mount. This engine commits that upper layer, stores it, and hashes
// it, so without this a directory a step copied into carries a fingerprint of
// the layers underneath it:
//
//	the same copy over two base images produces two layer digests
//	an observation of that directory goes stale whenever the base moves
//
// Green paper §3.3 lists what a layer records, and "which lower inode did this
// come from" is not on the list. It is a property of an assembly, not of a
// file (E132).
//
// Narrow on purpose. Dropping every extended attribute would be the mirror
// mistake and would lose a `setcap` grant on the way into an image, which is
// the defect E92 existed to fix.
// `com.apple.` is the same argument from the other direction, and the guest's
// copy already excludes it (`ours`): macOS stamps files with attributes of its
// own - `com.apple.provenance` appears on a file this engine's own tests just
// wrote - and hashing them makes a layer's identity depend on which machine
// captured it. The rule was in one of the two places that hash a tree.
func assembledBy(name string) bool {
	return strings.HasPrefix(name, "user.overlay.") ||
		strings.HasPrefix(name, "trusted.overlay.") ||
		strings.HasPrefix(name, "com.apple.")
}

// setXattrs restores extended attributes onto a freshly written path.
//
// Attempted rather than insisted upon. A filesystem that does not carry them,
// or a namespace a worker may not write (`trusted.*` needs privilege), makes
// this fail for reasons the layer is not wrong about - and the caller's digest
// check is what decides whether the result is usable, in the one place that can
// tell "could not" from "did not need to".
func setXattrs(p string, xs []xattr) error {
	for _, x := range xs {
		_ = unix.Lsetxattr(p, x.name, []byte(x.value), 0)
	}

	return nil
}

// observedOwner is the ownership a walk reads off the filesystem, behind a seam.
//
// **E313 needs two users and a test has one.** The fault is a tree whose files
// are owned by whoever unpacked it rather than by whoever packed it, which no
// single-user process can produce: an unprivileged chown to your own uid
// succeeds, so refusing it changes nothing. The first version of this test
// seamed the chown and passed with the bug present, which is the same class of
// mistake it exists to catch (E208).
//
// Seamed here because this is where the difference would show: the disk reports
// one owner and the stream declared another.
var observedOwner = func(uid, gid uint32) (uint32, uint32) { return uid, gid }
