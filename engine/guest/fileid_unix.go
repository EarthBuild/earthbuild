//go:build unix

package guest

import (
	"os"
	"syscall"
)

// idOf reads a file's device and inode.
//
// `syscall.Stat_t`, not `unix.Stat_t`: `filepath.Walk` hands back what
// `os.Lstat` produced and `os` fills in the former. The two are
// layout-identical and distinct types, and asserting the wrong one compiles and
// fails at runtime on exactly the files it exists for (E88).
func idOf(fi os.FileInfo) fileID {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Nlink < 2 {
		// A file with one link cannot be a hard link to anything, so it is not
		// worth remembering - which keeps the map to the size of the tree's
		// actually-linked files rather than the tree.
		return fileID{}
	}

	// This field is not this width on every platform.
	return fileID{dev: uint64(st.Dev), ino: st.Ino, ok: true} //nolint:unconvert
}
