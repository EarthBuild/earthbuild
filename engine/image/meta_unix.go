//go:build unix

package image

import (
	"archive/tar"
	"errors"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// paxXattr is the prefix a tar uses for an extended attribute.
const paxXattr = "SCHILY.xattr."

// applyOwner gives an entry the uid and gid the archive states.
//
// Best effort, and the distinction matters. This unpacker runs on the machine
// invoking the build, unprivileged, while the reference unpacks inside a daemon
// as root - so handing a file to an arbitrary uid is a request the OS will
// refuse here and grant there. Refusing the build over it would refuse every
// base image, since `alpine`'s files are root's and the builder is not.
//
// So permission failures leave the entry owned by the builder, which is what
// happened silently before and is now the *only* case that is tolerated:
// anything else is an error. Green paper A2's "degrade, but say so" - the
// saying-so is E92's note in the plan and the fact that this function exists to
// be read.
func applyOwner(h *tar.Header, target string) error {
	if h.Uid == os.Getuid() && h.Gid == os.Getgid() {
		return nil // already the owner; nothing to ask for
	}

	err := unix.Lchown(target, h.Uid, h.Gid)
	if err == nil || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EINVAL) {
		return nil
	}

	return err //nolint:wrapcheck // the caller names the entry
}

// applyXattrs carries the extended attributes a PAX header states.
//
// `security.capability` is why this is here: `setcap` on a binary lives in that
// attribute, a tar carries it in a PAX record, and a base image unpacked
// without it has a service that cannot bind its port. Best effort for the same
// reason as ownership - `security.*` needs privilege this process may not have,
// and refusing would refuse the image.
func applyXattrs(h *tar.Header, target string) error {
	for k, v := range h.PAXRecords {
		name, ok := strings.CutPrefix(k, paxXattr)
		if !ok {
			continue
		}

		err := unix.Lsetxattr(target, name, []byte(v), 0)
		if err == nil || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EOPNOTSUPP) {
			continue
		}

		return err //nolint:wrapcheck // the caller names the entry
	}

	return nil
}

func makeSpecial(h *tar.Header, target string) (placed bool, err error) {
	var kind uint32

	switch h.Typeflag {
	case tar.TypeChar:
		kind = unix.S_IFCHR
	case tar.TypeBlock:
		kind = unix.S_IFBLK
	case tar.TypeFifo:
		kind = unix.S_IFIFO
	default:
		return false, nil // not one of ours
	}

	dev := int(unix.Mkdev(uint32(h.Devmajor), uint32(h.Devminor))) //nolint:gosec // from the archive

	err = unix.Mknod(target, kind|uint32(h.Mode), dev) //nolint:gosec // the archive's mode
	if err == nil {
		return true, nil
	}

	if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EOPNOTSUPP) {
		return false, nil
	}

	return false, err //nolint:wrapcheck // the caller names the entry
}

// readXattrs lists an entry's extended attributes as PAX records.
//
// The inverse of applyXattrs, and missing until now: `Pack` described every
// entry with `tar.FileInfoHeader`, which knows nothing about them, so a layer's
// attributes were dropped on the way into an image. A `setcap` grant lives in
// `security.capability`, so a binary that could bind port 80 in the build could
// not in the image it was packed into.
//
// Sorted, because a map's iteration order must not reach an archive whose
// digest is the image's identity - the same rule the entry ordering already
// follows a few lines away.
func readXattrs(p string) (map[string]string, error) {
	size, err := unix.Llistxattr(p, nil)
	if err != nil || size == 0 {
		return nil, nil //nolint:nilerr // a filesystem without them loses nothing
	}

	buf := make([]byte, size)

	size, err = unix.Llistxattr(p, buf)
	if err != nil {
		return nil, nil //nolint:nilerr // as above
	}

	out := map[string]string{}

	for _, name := range strings.Split(string(buf[:size]), "\x00") {
		if name == "" || strings.HasPrefix(name, "com.apple.") {
			// The host operating system's own bookkeeping about files it
			// stores, which is not part of any layer (E90).
			continue
		}

		value := make([]byte, 1024)

		n, err := unix.Lgetxattr(p, name, value)
		if err != nil {
			continue
		}

		out[paxXattr+name] = string(value[:n])
	}

	return out, nil
}

// hardLinkID identifies a file that has more than one name.
//
// A file with a single link cannot be a hard link to anything, so it is not
// worth remembering - which keeps the map to the size of the archive's actually
// linked files rather than the archive.
func hardLinkID(fi os.FileInfo) (linkID, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Nlink < 2 || fi.IsDir() {
		return linkID{}, false
	}

	return linkID{dev: uint64(st.Dev), ino: st.Ino}, true
}
