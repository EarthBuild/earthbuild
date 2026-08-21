//go:build darwin

package exec

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

// cloneTree copies a whole directory hierarchy in one call, copy-on-write.
//
// **One syscall for the tree.** APFS's `clonefile(2)` accepts a directory and
// clones it recursively, sharing the underlying extents until something writes.
// Measured against a Go base image - 17,580 entries, 267MB - the alternatives
// are hard-linking each entry at 8.5s serial and 6s across every core, or
// per-file cloning at 4.1s. This is 0.26s.
//
// Copy-on-write is also the safer of the two. A hard link makes one file with
// two names, so a write through the layer store reaches into the shared image
// cache; a clone diverges on the first write, which is what a caller of a
// *copy* has every right to expect.
//
// Fails where the two paths are on different filesystems, where the filesystem
// is not APFS, and where the destination exists. All three are ordinary rather
// than exceptional, so the caller falls back rather than failing (see
// placeTree).
func cloneTree(src, dst string) error {
	return unix.Clonefile(filepath.Clean(src), filepath.Clean(dst), 0)
}
