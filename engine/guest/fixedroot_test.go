package guest_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// fixedRootMat materialises everything at one real directory, so a test can
// look at what a step actually wrote without needing overlayfs.
type fixedRootMat struct{ root string }

func (m *fixedRootMat) Materialise(context.Context, []ir.NodeID) (core.Handle, error) {
	return fixedHandle{m.root}, nil
}

type fixedHandle struct{ root string }

func (h fixedHandle) Delta() string { return h.root }

func (h fixedHandle) Root() string { return h.root }
func (h fixedHandle) Observations() core.Observation {
	return core.Observation{Reads: map[string]ir.NodeID{}, Listings: map[string]ir.NodeID{}}
}
func (h fixedHandle) Release() error { return nil }

// stepRoot is a directory to root a step in, and the wait its teardown needs.
//
// **A step's mounts outlive its response.** The guest binds `/etc/resolv.conf`
// and friends into the step's root, and tears them down when the step's own
// goroutine finishes - which is not ordered with the reply the caller already
// has. So `t.TempDir()`'s cleanup races the unmount and fails with:
//
//	TempDir RemoveAll cleanup: unlinkat …/etc/resolv.conf: device or resource busy
//
// Invisible until E122, because every test that could hit it was skipping on
// Linux and running as root inside a VM on macOS.
//
// The wait is an assertion, not a sleep: "the step eventually releases what it
// mounted" is a claim nothing else in this package makes, and a guest that
// leaked one mount per step would pass every other test here.
//
// Whether the *response* should be ordered after the teardown is a question for
// a maintainer and is recorded rather than decided: it would make a step's reply
// wait on unmounting, which is a cost paid by every step to tidy a case only a
// caller sharing the filesystem can see.
func stepRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	// Registered before anything mounts into it, so it runs after - cleanups
	// are LIFO, and this one has to happen before TempDir removes the tree.
	t.Cleanup(func() { waitReleased(t, root) })

	return root
}

// waitReleased blocks until everything a step mounted under root is gone.
//
// Removing the tree is the probe. A bind mount refuses `unlinkat` with EBUSY,
// so a `RemoveAll` that succeeds is proof that nothing is mounted under it -
// and there is no list of mount points to enumerate and get wrong. The first
// version waited on `/etc/resolv.conf` alone and the next run failed on
// `/dev/full`, which is that mistake in miniature.
//
// Bounded, because a mount that is never released must fail the test rather
// than hang it - a build's cleanup has the same property and for the same
// reason.
func waitReleased(t *testing.T, root string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
		err := os.RemoveAll(root)
		if err == nil {
			return
		}

		if !errors.Is(err, unix.EBUSY) {
			t.Errorf("cannot tell whether %s still holds mounts: %v", root, err)

			return
		}

		if time.Now().After(deadline) {
			t.Errorf("a step left mounts under %s after 10s: %v"+
				"\n  the guest tears a step's mounts down when the step ends, and one"+
				"\n  leaked mount per step is a long-running guest that runs out of them",
				root, err)

			return
		}

		time.Sleep(10 * time.Millisecond)
	}
}
