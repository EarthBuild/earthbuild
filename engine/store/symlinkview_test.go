package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A path reached through a symlinked directory is still in the layer.
//
// **This is why a layer is not indexed by walking it.** An index of what a
// layer holds, built from a walk and consulted to skip layers that cannot
// answer, looked like the fix for a lookup that probes every layer - and it is
// blind to exactly this: `/bin -> usr/bin` is how most images are laid out, the
// kernel resolves the path, and the walk never names `bin/busybox`. The layer
// was skipped, the file was reported gone from the base, and a build went from
// 61 cache hits to none.
//
// It also measured no benefit, because what a lookup costs is not the stats it
// makes on the way but the file it opens and hashes when it arrives. Kept as a
// guard: the next person to think of indexing a layer should meet this first.
func TestAPathThroughASymlinkedDirectoryIsFound(t *testing.T) {
	store := t.TempDir()

	id := (&ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"sym"}}}).ID()
	root := filepath.Join(store, "layers", id.String())

	err := os.MkdirAll(filepath.Join(root, "usr", "bin"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(root, "usr", "bin", "busybox"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// /bin -> usr/bin, which is how most images are laid out.
	err = os.Symlink("usr/bin", filepath.Join(root, "bin"))
	if err != nil {
		t.Fatal(err)
	}

	view, err := LayerStore(store).View(context.Background(), []ir.NodeID{id})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := view.Digest("/bin/busybox"); !ok {
		t.Error("a path reached through a symlinked directory is reported gone;" +
			" the kernel resolves it and a walk of the layer never names it")
	}
}
