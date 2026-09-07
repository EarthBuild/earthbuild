//go:build linux

package overlay_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/mat/overlay"
	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// Two materialisers over one scratch directory do not share a mount.
//
// Mount directories were named `h<runID>-<counter>`, with `runID = os.Getpid()`
// and the counter starting at one in every materialiser. The comment on it is
// about a *dead* process: a killed guest leaves its mounts behind, so the next
// guest asking for `h000001` finds them and overlayfs answers EBUSY. Naming the
// run as well as the handle declines to collide with the dead.
//
// It does not decline to collide with the **living**, and on Linux the guest
// runs in a PID namespace - so `os.Getpid()` is 1 for every guest there has
// ever been. Two builds sharing a store get `h1-000001` each, land in one
// overlay, and both steps write into one upper directory.
//
// Measured: two builds of two targets over a shared base, run at once, and both
// artifacts came back holding *both* steps' output:
//
//	one.txt   shared\ntwo\none
//	two.txt   shared\ntwo\none
//
// Two builds that both succeeded and both produced the wrong bytes, which is
// the failure mode a shared cache has and an unshared one does not (E140).
//
// The fix is to stop deriving a name that has to be unique and **ask the
// filesystem for one**, which is the only party that can guarantee it. That
// also subsumes the dead-process case the original comment was about: a name
// nobody has is a name no corpse is holding.
func TestTwoMaterialisersDoNotShareAMount(t *testing.T) { //nolint:paralleltest // mounts
	if !nstest.In(t) {
		return
	}

	scratch := t.TempDir()

	id := ir.NodeID{1}

	a, err := overlay.NewSplit(t.TempDir(), scratch)
	if err != nil {
		t.Skipf("no overlay materialiser here: %v", err)
	}

	b, err := overlay.NewSplit(t.TempDir(), scratch)
	if err != nil {
		t.Fatal(err)
	}

	err = a.WriteLayer(id, map[string]string{"f": "x"})
	if err != nil {
		t.Fatal(err)
	}

	err = b.WriteLayer(id, map[string]string{"f": "x"})
	if err != nil {
		t.Fatal(err)
	}

	ha, err := a.Materialise(context.Background(), []ir.NodeID{id})
	if err != nil {
		t.Skipf("cannot mount here: %v", err)
	}

	defer func() { _ = ha.Release() }()

	hb, err := b.Materialise(context.Background(), []ir.NodeID{id})
	if err != nil {
		t.Fatalf("the second materialiser could not mount beside the first: %v", err)
	}

	defer func() { _ = hb.Release() }()

	if ha.Root() == hb.Root() {
		t.Errorf("two materialisers sharing a scratch directory mounted at one"+
			" root: %s\n  two builds then write into one upper directory and both"+
			" produce the other's output", ha.Root())
	}

	// And the deltas are separate, which is what actually goes wrong: a shared
	// root means a shared upper, and a shared upper means each build commits
	// the other's writes.
	if ha.Delta() == hb.Delta() {
		t.Errorf("two materialisers share an upper directory: %s", ha.Delta())
	}
}
