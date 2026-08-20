//go:build linux

package exec_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/mat/overlay"
	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// What the observer digests and what the view digests are the same number.
//
// This is the join the whole L2 tier rests on and nothing had checked it.
//
//	the guest observes   layer.PathDigest(<inside the mounted overlay>)
//	the view answers     layer.PathDigest(<inside a layer directory>)
//
// `Consistent` compares those two values. They are produced by one function
// (E114, deliberately) but applied to **two different filesystems**, and
// `layer.PathDigest` hashes every extended attribute it finds. overlayfs keeps
// its own bookkeeping in xattrs - `trusted.overlay.opaque`, `user.overlay.*`
// under `userxattr` (E109) - and a merged directory can carry one where the
// layer directory underneath does not.
//
// If they disagree, every prediction fails against every base: **L2 never hits
// and nothing is ever wrong**, which is the failure mode that reads as the
// feature being worthless rather than broken, and which no test of either side
// alone can find.
func TestTheObserverAndTheViewAgree(t *testing.T) { //nolint:paralleltest // mounts
	// An unprivileged overlay needs a user namespace, and `go test` does not
	// run in one. Without this the test skips unless somebody remembers to
	// invoke the binary under `unshare -Umr`, and a skip that depends on how
	// the binary was invoked is not coverage.
	if !nstest.In(t) {
		return
	}

	store := t.TempDir()

	m, err := overlay.New(store)
	if err != nil {
		t.Skipf("no overlay materialiser here: %v", err)
	}

	lower := ir.NodeID{1}
	upper := ir.NodeID{2}

	err = m.WriteLayer(lower, map[string]string{"w/keep": "x\n", "etc/hosts": "127.0.0.1\n"})
	if err != nil {
		t.Fatal(err)
	}

	// A second layer, so the merged view is genuinely assembled rather than a
	// single directory shown through a mount that changes nothing.
	err = m.WriteLayer(upper, map[string]string{testTool: "new\n"})
	if err != nil {
		t.Fatal(err)
	}

	stack := []ir.NodeID{lower, upper}

	h, err := m.Materialise(context.Background(), stack)
	if err != nil {
		t.Skipf("cannot mount an overlay here: %v", err)
	}

	defer func() { _ = h.Release() }()

	view, err := exec.LayerStore(store).View(context.Background(), stack)
	if err != nil {
		t.Fatal(err)
	}

	// A directory and a file, because the xattr risk is on directories and the
	// content risk is on files, and one of each is the smallest fixture that
	// covers both.
	for _, path := range []string{"/w", "/etc/hosts", "/usr/tool"} {
		t.Run(path, func(t *testing.T) {
			observed, err := layer.PathDigest(filepath.Join(h.Root(), filepath.Clean("/"+path)))
			if err != nil {
				t.Fatalf("the observer cannot digest %s in the mounted overlay: %v", path, err)
			}

			seen, ok := view.Digest(path)
			if !ok {
				t.Fatalf("the view says %s is absent, and the observer just read it", path)
			}

			if observed != seen {
				t.Errorf("the observer and the view disagree about %s:"+
					"\n  observed %s   (inside the mount)"+
					"\n  view     %s   (inside the layer store)"+
					"\n  Consistent compares these, so every prediction about this"+
					" path fails and L2 never hits", path, observed, seen)
			}
		})
	}
}
