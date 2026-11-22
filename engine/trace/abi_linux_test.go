//go:build linux

package trace

import (
	"encoding/binary"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The structures are the size the kernel says they are.
//
// An ABI mistake here is silent. A field read from the wrong offset yields a pid
// that is really half an instruction pointer, and the engine goes on to record
// observations about a process that does not exist - no crash, no error, a
// prediction keyed on nonsense. Reviewing a struct against a header catches that
// once, on the day somebody looks.
//
// **The kernel states the size itself.** An ioctl request encodes the size of
// its argument in bits 16..29, so `SECCOMP_IOCTL_NOTIF_RECV` *is* the assertion:
// whatever this engine declares must be 80 bytes because the constant says 80.
// Nothing here is written down twice, which is what makes it a check rather than
// a second opinion.
//
// Both sizes are asserted, and they answer different questions. `binary.Size` is
// the packed width of the fields; `unsafe.Sizeof` is what the compiler lays out.
// They agree only when there is no padding, so comparing each to the kernel's
// number catches a field of the wrong width *and* a field inserted where the
// alignment rules would open a hole - and the second is the one a reader would
// not see.
func TestTheNotificationStructuresAreTheSizeTheKernelSays(t *testing.T) {
	t.Parallel()

	wantNotif, wantResp := notifSizes(unix.SECCOMP_IOCTL_NOTIF_RECV,
		unix.SECCOMP_IOCTL_NOTIF_SEND)

	for _, tc := range []struct {
		name   string
		want   int
		packed int
		laid   int
	}{
		{
			name: "seccomp_notif", want: wantNotif,
			packed: binary.Size(seccompNotif{}),
			laid:   int(unsafe.Sizeof(seccompNotif{})),
		},
		{
			name: "seccomp_notif_resp", want: wantResp,
			packed: binary.Size(seccompNotifResp{}),
			laid:   int(unsafe.Sizeof(seccompNotifResp{})),
		},
	} {
		if tc.packed != tc.want {
			t.Errorf("%s packs to %d bytes; the kernel's ioctl says %d",
				tc.name, tc.packed, tc.want)
		}

		if tc.laid != tc.want {
			t.Errorf("%s occupies %d bytes; the kernel's ioctl says %d"+
				" - a field is padded, so the layout has a hole the kernel"+
				" does not", tc.name, tc.laid, tc.want)
		}
	}

	// And the decoder itself, against numbers taken from the kernel headers by
	// hand. Without this, a `notifSizes` that returned zero would make every
	// assertion above compare zero to zero and pass.
	if wantNotif != 80 || wantResp != 24 {
		t.Errorf("the sizes decoded out of the ioctl requests are %d and %d,"+
			" and linux/seccomp.h says 80 and 24 - the decoder is wrong,"+
			" not the structures", wantNotif, wantResp)
	}
}

// seccomp_data is 64 bytes, which is the half of the above that can drift alone.
//
// It is embedded, so an error inside it moves `seccompNotif` too and the test
// above would catch it. Asserted separately because *which* structure is wrong
// is the first thing anybody debugging this needs, and "one of these two is 8
// bytes out" is a worse place to start from.
func TestTheSyscallRecordIsSixtyFourBytes(t *testing.T) {
	t.Parallel()

	const want = 64

	if got := int(unsafe.Sizeof(seccompData{})); got != want {
		t.Errorf("seccomp_data occupies %d bytes, want %d", got, want)
	}
}
