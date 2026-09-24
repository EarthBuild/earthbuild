//go:build linux

package fsclone

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Range moves size bytes from in to out with the kernel doing the moving, and
// says whether it managed.
//
// **On btrfs and XFS this is a reflink**: the two files share extents and the
// copy costs nothing until one side is written. That is the whole reason a
// microVM's store is XFS with `reflink=1` - a captured layer is mostly the
// bytes its base already had, and copying them again is the difference between
// a store that grows by what a step changed and one that grows by what a step
// could see. One test group filled sixty-three gigabytes doing the latter.
//
// On ext4 it still copies, but in the kernel, so the bytes do not pass through
// this process.
//
// Looped, because one call is permitted to copy less than it was asked for and
// a short copy taken for a whole one is a truncated file that nothing reports.
//
// Failure is not an error: a source and destination on different filesystems, a
// kernel older than 4.5, and a /proc file whose size cannot be known are all
// ordinary, and the caller's answer to each is the copy it was going to make
// anyway.
func Range(in, out *os.File, size int64) bool {
	for done := int64(0); done < size; {
		n, err := unix.CopyFileRange(int(in.Fd()), nil, int(out.Fd()), nil, int(size-done), 0)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case err != nil:
			return false
		case n == 0:
			// Nothing copied and no error means the source ended sooner than
			// its size promised. Reporting success would leave a short file.
			return done == size
		}

		done += int64(n)
	}

	return true
}
