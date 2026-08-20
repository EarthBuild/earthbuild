package guest

import (
	"testing"

	"golang.org/x/sys/unix"
)

// A remount never asks for two atime policies at once.
//
// Inside a user namespace the kernel *locks* a mount's flags - `MS_RDONLY`,
// `MS_NOSUID`, `MS_NODEV`, `MS_NOEXEC` and the atime flags - and refuses a
// remount that would clear one. So the read-only remount has to repeat what the
// mount already has, which is what `lockedFlags` is for.
//
// `MS_NOATIME` and `MS_RELATIME` are **mutually exclusive**, and a remount
// carrying both is refused with EINVAL. `statfs` can report `ST_NOATIME` and
// `ST_RELATIME` together, so repeating both is repeating something the kernel
// will not accept.
//
// That is the flake: one full Linux run in four failed with
//
//	make /etc/resolv.conf read-only: invalid argument
//
// and never in isolation, because which filesystem `/etc/resolv.conf` sits on -
// and therefore which atime bits statfs reports - is not the same on every run.
// An intermittent failure whose cause is a *pair* of flags looks like a race
// and is not one.
func TestARemountAsksForOneAtimePolicy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		given int
		want  int
	}{
		{"nothing", 0, 0},
		{"the locked triple", unix.ST_NODEV | unix.ST_NOSUID | unix.ST_NOEXEC,
			unix.MS_NODEV | unix.MS_NOSUID | unix.MS_NOEXEC},
		{"relatime alone", unix.ST_RELATIME, unix.MS_RELATIME},
		{"noatime alone", unix.ST_NOATIME, unix.MS_NOATIME},
		// The pair the kernel refuses. noatime is the stronger policy and the
		// one to keep: a mount that never updates access times satisfies
		// anything relatime would have asked for.
		{"both", unix.ST_NOATIME | unix.ST_RELATIME, unix.MS_NOATIME},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := mountFlagsOf(tc.given)
			if got != tc.want {
				t.Errorf("mountFlagsOf(%#x) = %#x, want %#x", tc.given, got, tc.want)
			}

			if got&unix.MS_NOATIME != 0 && got&unix.MS_RELATIME != 0 {
				t.Errorf("both atime flags at once (%#x): the kernel answers EINVAL"+
					" and the caller reports it as a failed remount", got)
			}
		})
	}
}
