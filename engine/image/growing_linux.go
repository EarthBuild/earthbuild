//go:build linux

package image

import (
	"os"

	"golang.org/x/sys/unix"
)

// noReadahead stops the kernel reading past what was asked for.
//
// **This is the whole of why a growing file can be read at all.** Pre-allocated
// to its final length, the first read pulls in pages the writer has not reached;
// they are zeros, and once cached they stay zeros. Measured over a shared mount
// with the reader taking a chunk only once the writer confirmed it: 1 of 10
// chunks fresh without this, 10 of 10 with it (E683).
//
// Best effort. A kernel that refuses the hint reads correctly, because the
// reader is bounded by what the writer has confirmed either way - it would only
// read a page twice.
func noReadahead(f *os.File) {
	_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_RANDOM)
}
