package guest

import "testing"

// The devices are given a directory of their own, before any of them is bound.
//
// **Not tidiness - depth.** A bind needs a file to land on, and creating one
// inside the step's merged overlay makes overlayfs materialise the parent
// directory in the upper layer first, which means reading it through every
// lower layer. The first bind into a directory pays for that directory; the
// five that follow it do not. So six device nodes bound straight into the
// overlay cost time proportional to how deep the build already is, on every
// step, which makes a build quadratic in its own length (E635, E636).
//
// An empty directory over `/dev` costs nothing to mount - `/dev` is already
// there, so nothing has to be created - and the six binds then land in it
// rather than in the overlay. Measured on twenty steps: 31.7ms of binding per
// step became 17.4ms, and a step 54.5ms became 39.2ms (E637).
//
// Ordering is the mechanism, which is why this asserts on it: `bindMounts`
// works through the list in order, so a `/dev` that arrived anywhere but first
// would be mounted *over* the devices already bound beneath it, and the step
// would see an empty `/dev`.
func TestTheDevicesAreGivenARoomOfTheirOwn(t *testing.T) {
	t.Parallel()

	mounts := deviceMounts()
	if len(mounts) == 0 {
		t.Skip("this machine has none of the device files")
	}

	first := mounts[0]

	if first.Target != "/dev" {
		t.Fatalf("the first device mount is %q, and it has to be /dev"+
			"\n  the rest land inside it, so anything else is bound into the"+
			" step's overlay and pays for the depth beneath it", first.Target)
	}

	if !first.Ephemeral {
		t.Error("/dev is not ephemeral, so it is not a directory of its own" +
			"\n  the devices below it are then created in the overlay again")
	}

	// And every device is *under* it, or it is not doing anything for them.
	for _, m := range mounts[1:] {
		if len(m.Target) < 6 || m.Target[:5] != "/dev/" {
			t.Errorf("%s is not under /dev, so the room does not hold it", m.Target)
		}
	}
}
