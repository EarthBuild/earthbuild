//go:build linux

package overlay_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/mat/overlay"
	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// The upper and work directories must live on the guest's own filesystem, not
// alongside the layers.
//
// Layers arrive over a shared mount - virtiofs, from the host's CAS - and
// overlayfs cannot use virtiofs as an upper layer: it lacks trusted xattr
// support, so the kernel silently falls back to a read-only mount and the first
// write a step attempts fails with "read-only file system". Separating them also
// means a step cannot write into the shared cache at all.
func TestScratchIsSeparateFromLayers(t *testing.T) {
	t.Parallel()

	// Mounts, so it needs the namespace where CAP_SYS_ADMIN applies (E98). It
	// skipped for want of one, which meant the property it guards - that a step
	// cannot write into the shared layer cache - was never checked on the
	// platform that has the cache.
	if !nstest.In(t) {
		return
	}

	layers, scratch := t.TempDir(), t.TempDir()

	m, err := overlay.NewSplit(layers, scratch)
	if err != nil {
		t.Fatal(err)
	}

	id := ir.NodeID{1}
	err = m.WriteLayer(id, map[string]string{"f": "x"})
	if err != nil {
		t.Fatal(err)
	}

	h, err := m.Materialise(t.Context(), []ir.NodeID{id})
	if err != nil {
		t.Skipf("overlay unavailable here: %v", err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	// The merged root must sit under scratch, so that everything written during
	// the step lands on the guest's own filesystem.
	if !strings.HasPrefix(h.Root(), scratch) {
		t.Errorf("mount root %s is not under the scratch directory %s", h.Root(), scratch)
	}

	entries, err := os.ReadDir(filepath.Join(layers, "mounts"))
	if err == nil && len(entries) > 0 {
		t.Errorf("%d mount directories were created alongside the layers", len(entries))
	}
}
