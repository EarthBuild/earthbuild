//go:build linux

package guest_test

import (
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/coretest"
	"github.com/EarthBuild/earthbuild/engine/mat/overlay"
	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// overlayViaGuest wraps the real overlayfs materialiser so the content tests -
// the ones the simulator legitimately skips - run across the wire.
//
// This is the arrangement earth-guestd actually ships as: a real materialiser
// inside the VM, reached over the protocol. The only difference in production
// is that the pipe is the VM's channel.
type overlayViaGuest struct {
	core.Materialiser

	inner *overlay.Materialiser
}

func (o overlayViaGuest) WriteLayer(id core.Key, files map[string]string) error {
	return o.inner.WriteLayer(id, files)
}

func TestOverlayThroughTheGuestProtocol(t *testing.T) {
	t.Parallel()

	// Not euid: the capability is checked in the namespace the mount happens
	// in, so a user namespace grants it (E98). The third copy of this belief to
	// be found, after `Native.Available` and `TestOverlayConforms` - and this
	// one gates the overlay materialiser *reached over the guest protocol*,
	// which is exactly the arrangement production uses.
	if !nstest.In(t) {
		return
	}

	// And root is not enough inside a container, whose own root is overlayfs.
	// See the same guard in engine/mat/overlay: an error that is not
	// ErrUnavailable is a defect and must not become a skip.
	// See the same guard in engine/mat/overlay: a tmpfs is tried when the temp
	// dir is on overlayfs, and an error that is not ErrUnavailable is a defect
	// rather than a skip (E69).
	root, done, err := overlay.Mountable(t.TempDir())
	if err != nil {
		if errors.Is(err, overlay.ErrUnavailable) {
			t.Skipf("overlayfs cannot be mounted anywhere here: %v", err)
		}

		t.Fatalf("overlayfs is available and did not work: %v", err)
	}

	t.Cleanup(done)

	coretest.MaterialiserSuite(t, func(t *testing.T) (core.Materialiser, func()) {
		t.Helper()

		m, err := overlay.New(root)
		if err != nil {
			t.Fatal(err)
		}

		return overlayViaGuest{Materialiser: pair(t, m), inner: m}, func() {}
	})
}
