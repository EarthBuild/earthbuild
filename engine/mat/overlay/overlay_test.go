package overlay_test

import (
	"errors"
	"os"
	"runtime"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/coretest"
	"github.com/EarthBuild/earthbuild/engine/mat/overlay"
	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// TestOverlayConforms runs the same contract the simulator passes, against the
// real thing. That is the whole purpose of a conformance suite: the claim
// "this is a materialiser" means "it passes this".
func TestOverlayConforms(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("overlayfs is linux-only")
	}

	// **Not euid.** E13 measured that mounting needs CAP_SYS_ADMIN and this
	// concluded "so, root". E98 measured the rest of it: the capability is
	// checked in the namespace the mount happens in, and a user namespace
	// grants it there while granting nothing on the host - which is how every
	// rootless container runtime works, and which `Native.Available` was
	// changed to reflect.
	//
	// It was changed there and not here, so the conformance suite for the
	// materialiser S3 calls "real on Linux" has never run unprivileged - the
	// session's recurring shape, a fix applied to one of the two places it
	// holds. nstest.In re-runs this test inside a namespace, where it can mount.
	if !nstest.In(t) {
		return
	}

	// Root is not enough. Inside a container the working directory is itself on
	// overlayfs, which overlayfs will not stack on - which is where the
	// repository's own `+unit-test` runs, so the suite failed there on a
	// property of the machine (E52).
	//
	// Asked by mounting, and skipping only for the answer that means "not
	// here": an error that is *not* ErrUnavailable is this materialiser being
	// broken, and laundering that into a skip would retire the conformance
	// suite without anybody deciding to.
	// Mountable tries a tmpfs when the temp dir is on overlayfs, which is the
	// engine's own advice in that case and the difference between running this
	// suite in CI and skipping it there (E69).
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

		// Under the materialiser's root, which the suite hands in; t.TempDir
		// would put each case somewhere else entirely.
		dir, err := os.MkdirTemp(root, "case-*") //nolint:usetesting // see above
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = os.RemoveAll(dir) })

		m, err := overlay.New(dir)
		if err != nil {
			t.Fatal(err)
		}

		return m, func() {}
	})
}
