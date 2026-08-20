//go:build linux

package guest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// copySpecial reproduces a device or fifo at dst.
//
// Overlayfs records a deletion as a **character device, 0/0**, sitting where
// the removed entry was, and marks a directory that replaces a lower one as
// opaque with an xattr. Both are how the upper layer says "this is gone", and a
// copy that drops them produces a layer claiming nothing was removed.
//
// The previous behaviour was to skip, with a comment saying devices "rarely
// appear in a delta". They appear in the delta of every step that cleans up
// after itself, which is most of them.
//
// Mknod needs privilege and has it: the guest runs as root inside the VM, which
// is the only place a delta is committed.
func copySpecial(src, dst, name string, fi os.FileInfo) (placed bool, err error) {
	// syscall's, not unix's. `filepath.Walk` hands back what `os.Lstat`
	// produced, and os fills in `*syscall.Stat_t`; the two are layout-identical
	// and distinct types, so the assertion for the wrong one fails at runtime
	// on exactly the entries this function exists for.
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("cannot read the device numbers of %s", src)
	}

	err = unix.Mknod(dst, uint32(fi.Mode().Perm())|deviceBits(fi.Mode()), int(st.Rdev))
	if err == nil {
		return true, nil
	}

	// A store that cannot hold a device node cannot hold a deletion, and this
	// engine will not pretend otherwise. The layer store is a host directory
	// shared into the sandbox (E1b), and a macOS host refuses mknod through
	// that share exactly as it refuses to carry uids (E84) - the same
	// architectural choice, showing its cost a second way.
	//
	// Refusing is green paper A2 and I10: the alternative is what this code did
	// until now, which is to drop the entry and report success, so a step's
	// `rm` had no effect and the image still contained what the author removed.
	// A whiteout - a character device 0:0 - is a *deletion*, and a deletion has
	// a portable spelling that every registry already uses. Written that way
	// where the store cannot hold a device node, and turned back into one by
	// the materialiser on storage inside the VM (E94).
	//
	// Only for whiteouts. Any other device is a real device, `.wh.` would not
	// mean it, and the refusal below still stands.
	if (errors.Is(err, unix.EPERM) || errors.Is(err, unix.EOPNOTSUPP)) && isWhiteout(fi, st) {
		// Nothing is placed *at* dst: the marker is a sibling, so the caller
		// must not go on to stamp a path that does not exist.
		return false, writeWhiteout(dst)
	}

	if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EOPNOTSUPP) {
		return false, fmt.Errorf(
			"this step deletes %s, and the layer store cannot record a deletion here"+
				"\n  a removal is stored as a device node, and %s refuses to create one:"+
				" %w"+
				"\n  the store is a host directory shared into the sandbox, and this host's"+
				"\n  filesystem has no device nodes to share"+
				"\n  builds that delete nothing are unaffected; the rest need a store on a"+
				"\n  Linux filesystem",
			name, filepath.Dir(dst), err)
	}

	return false, fmt.Errorf("recreate %s: %w", dst, err)
}

// deviceBits is the type half of a mode, which Mknod needs and os.FileMode
// spells differently.
func deviceBits(m os.FileMode) uint32 {
	switch {
	case m&os.ModeCharDevice != 0:
		return unix.S_IFCHR
	case m&os.ModeDevice != 0:
		return unix.S_IFBLK
	case m&os.ModeNamedPipe != 0:
		return unix.S_IFIFO
	case m&os.ModeSocket != 0:
		// A socket in a delta is a step's own runtime artefact and cannot be
		// meaningfully recreated; treated as a fifo so the entry exists rather
		// than vanishing, which is the failure this whole change is about.
		return unix.S_IFIFO
	default:
		return unix.S_IFREG
	}
}

// isWhiteout reports whether an entry is overlayfs's record of a deletion:
// a character device with both numbers zero.
func isWhiteout(fi os.FileInfo, st *syscall.Stat_t) bool {
	return fi.Mode()&os.ModeCharDevice != 0 && st.Rdev == 0
}
