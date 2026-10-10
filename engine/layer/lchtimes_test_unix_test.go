//go:build unix

package layer_test

import (
	"time"

	"golang.org/x/sys/unix"
)

// lchtimesForTest stamps a symlink itself, so the fixture can give a link and
// its target different times - which is the whole of what the round trip has to
// preserve.
func lchtimesForTest(p string, when time.Time) error {
	ts := []unix.Timespec{
		unix.NsecToTimespec(when.UnixNano()),
		unix.NsecToTimespec(when.UnixNano()),
	}

	return unix.UtimesNanoAt(unix.AT_FDCWD, p, ts, unix.AT_SYMLINK_NOFOLLOW) //nolint:wrapcheck // a fixture
}
