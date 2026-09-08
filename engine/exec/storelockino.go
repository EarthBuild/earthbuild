package exec

import (
	"os"
	"syscall"
)

// storeInode is the inode number of a path, where the platform has one.
func storeInode(at string) (uint64, bool) {
	fi, err := os.Stat(at)
	if err != nil {
		return 0, false
	}

	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}

	return uint64(st.Ino), true //nolint:unconvert // Ino is not uint64 everywhere
}
