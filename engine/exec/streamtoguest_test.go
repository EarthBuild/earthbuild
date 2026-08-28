package exec

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// TestWhetherAGuestUnpacksWhileTheHostIsStillFetching.
//
// **The default follows the store, because that is what made it pay.** Handing
// a guest a blob it can read as it arrives was measured as an exact wash and
// left off (E688): the head start it won was given straight back waiting on a
// progress marker read from the shared mount, about 460ms stale.
//
// Two things moved since. The marker came off the filesystem and onto the
// fault-in socket, which is guest-to-host already; and the store moved onto the
// guest's own device, which made the unpack fast enough for the head start to
// be worth having. Eight alternating cold pairs, every one of them the same
// way: 5751ms against 4401ms at the median (E810).
//
// So it is on wherever the guest unpacks and off everywhere else - not because
// it would be wrong elsewhere, but because it starts a fault-in relay that a
// build with no guest to relay to has no use for.
func TestWhetherAGuestUnpacksWhileTheHostIsStillFetching(t *testing.T) {
	t.Setenv(EnvStreamToGuest, "")

	if got, want := streamToGuest(), UnpacksInGuest(); got != want {
		t.Fatalf("with nothing set, streaming to the guest = %v, want %v"+
			"\n  it applies only where the guest unpacks, so that is what it follows",
			got, want)
	}
}

// TestAskingNotToStreamToTheGuest.
//
// **A hang is the failure this switch has, so turning it off has to work.** A
// guest reading a blob as it arrives waits on a writer it cannot see; that wait
// is bounded and says what it was waiting for, but the way to answer "is this
// what stopped my build" is to take the streaming away without rebuilding the
// engine. Pinned because empty no longer means off.
func TestAskingNotToStreamToTheGuest(t *testing.T) {
	t.Setenv(guest.EnvStoreInVM, "1")

	for _, c := range []struct {
		set  string
		want bool
	}{
		{"0", false},
		{"false", false},
		{"no", false},
		{"1", true},
		{"true", true},
		{"yes", true},
	} {
		t.Run(c.set, func(t *testing.T) {
			t.Setenv(EnvStreamToGuest, c.set)
			if got := streamToGuest(); got != c.want {
				t.Fatalf("%s=%q streams to the guest = %v, want %v",
					EnvStreamToGuest, c.set, got, c.want)
			}
		})
	}
}
