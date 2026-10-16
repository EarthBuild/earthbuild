//go:build unix

package fstime

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// Lchtimes sets a path's own times, without following a symlink.
//
// `os.Chtimes` follows, so on a link it stamps the *target* - which changes a
// file the layer also carries and leaves the link with whatever time it was
// created. A layer's identity covers both (green paper §3.3), so the mistake is
// two wrong entries from one call.
//
// **"Mode and time apply to the link's target" is true of `Chmod` and
// `Chtimes`, and is not true of times.** A symlink has an mtime of its own and
// every walk records it. Three unpackers reasoned from that sentence to "there
// is nothing to set here", and each left a tree that digested differently after
// being copied: the guest's `copyTree` (E87-E90), the layer restorer (E262),
// and the image unpacker, where it meant no two machines ever agreed about a
// base image (E546).
//
// Two times rather than one, matching `os.Chtimes`, so that a single rule can
// check both spellings at every call site - which is what the clamp guard does.
func Lchtimes(path string, atime, mtime time.Time) error {
	ts := []unix.Timespec{
		unix.NsecToTimespec(atime.UnixNano()),
		unix.NsecToTimespec(mtime.UnixNano()),
	}

	err := unix.UtimesNanoAt(unix.AT_FDCWD, path, ts, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil {
		return fmt.Errorf("set the times on %s: %w", path, err)
	}

	return nil
}
