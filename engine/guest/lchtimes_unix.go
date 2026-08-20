//go:build unix

package guest

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// lchtimes sets a path's mtime without following a symlink.
//
// `os.Chtimes` follows, which is why `copyTree`'s symlink branch set no time at
// all - "mode and time would apply to the link's target, not the link" is true
// of `Chtimes` and was read as though it were true of timestamps.
//
// A symlink has an mtime of its own and `layer.Take` records it, via `Lstat`,
// like every other entry. So the digest carried it and the copy did not, and a
// tree containing one link digested differently after being copied. Found by
// the conformance test on its first run, which is the argument for having
// written it: four of these were found one at a time by following odd digests
// (E87 to E90), and the fifth took one command.
// Two times rather than one, matching os.Chtimes so that a single rule can
// check both spellings at every call site.
func lchtimes(path string, atime, mtime time.Time) error {
	ts := []unix.Timespec{
		unix.NsecToTimespec(atime.UnixNano()),
		unix.NsecToTimespec(mtime.UnixNano()),
	}

	err := unix.UtimesNanoAt(unix.AT_FDCWD, path, ts, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil {
		return fmt.Errorf("set the mtime on %s: %w", path, err)
	}

	return nil
}
