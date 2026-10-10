//go:build unix

package layer

import (
	"testing"

	"golang.org/x/sys/unix"
)

// TestTheArchiveReadersDeviceNumberIsThePlatformsOwn.
//
// **`major<<8 | minor` is not how any of these platforms encodes a device
// number.** Linux packs the high bits of both fields elsewhere in a 64-bit word;
// macOS uses `major<<24 | minor`. The unpacker calls `unix.Mkdev` and the walk
// reads back whatever the kernel then reports, so an archive reader that spelt
// it out by hand agreed with neither - and a layer carrying `/dev/null` would
// read one way from its blob and another from its tree.
//
// It is not caught by the fifo test, because a fifo's device numbers are zero
// and every encoding agrees about zero. It needs root to catch end to end, which
// this test does not have, so the encoding is pinned against the platform's own
// function instead.
func TestTheArchiveReadersDeviceNumberIsThePlatformsOwn(t *testing.T) {
	t.Parallel()

	for _, d := range []struct{ major, minor uint32 }{
		{0, 0},
		{1, 3},     // /dev/null
		{5, 1},     // /dev/console
		{136, 0},   // a pty, where the major exceeds a byte
		{4096, 7},  // beyond twelve bits, which Linux packs elsewhere
		{7, 4096},  // and the same for the minor
		{259, 300}, // both past a byte at once
	} {
		want := unix.Mkdev(d.major, d.minor)
		if got := mkdev(d.major, d.minor); got != want {
			t.Errorf("mkdev(%d, %d) = %#x, want %#x", d.major, d.minor, got, want)
		}
	}
}
