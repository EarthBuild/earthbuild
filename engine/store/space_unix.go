//go:build unix

package store

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// freeOn reports the bytes available to this user on the filesystem holding
// path.
//
// Available rather than free: the reserved blocks a filesystem keeps for root
// are not space a build can have, and reporting them is how a diagnostic ends up
// insisting there is room while the write keeps failing.
func freeOn(path string) (uint64, error) {
	var st unix.Statfs_t

	err := unix.Statfs(path, &st)
	if err != nil {
		return 0, fmt.Errorf("ask the filesystem at %s how much is left: %w", path, err)
	}

	// Both fields are small non-negative quantities whose types differ by
	// platform, which is the one thing this conversion is for.
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:gosec,unconvert // see above
}

// occupies is what a file costs the disk, not what it contains.
//
// **A store of small files costs far more than it holds.** Measured on this
// repository's own store: 857,948 files, 2.00 GiB of content, 5.11 GiB of
// blocks - because a one-byte file still takes a block, and a layer store is
// mostly small files. Sizing by content told somebody asking to be kept under
// 2 GiB that they were, while the disk gave up 5.11 GiB (E574).
//
// Directories count too, for the same reason and more so: there are a great many
// of them here and each one is a block that `rm` gives back.
func occupies(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Blocks >= 0 {
		return uint64(st.Blocks) * 512
	}

	return apparent(fi)
}
