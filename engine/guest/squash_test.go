package guest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A squash asked for over the wire is the squash the host would have made.
//
// Φ replaces a range of the stack with one identity so what remains can be
// mounted, and that identity is derived from the range - so the guest is told
// what the result is called and must produce exactly it. A guest that merged
// differently would file a layer under a name that does not describe it, which
// every other machine would then disagree with (E557).
// overridden is the file both layers write, where the merge order decides.
const overridden = "a.txt"

func TestASquashOverTheWireIsTheSquashTheHostWouldMake(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Two layers, the later one overriding a file of the earlier: the case the
	// merge order decides.
	older, newer := ir.NodeID{1}, ir.NodeID{2}

	write(t, root, older, map[string]string{overridden: "from the older", "keep.txt": "only here"})
	write(t, root, newer, map[string]string{overridden: "from the newer"})

	into := ir.NodeID{3}

	c := pairWith(t, &guest.Server{LayerDir: root})

	err := c.Squash(context.Background(), into, []ir.NodeID{older, newer})
	if err != nil {
		t.Fatal(err)
	}

	at := store.LayerStore(root).Path(into)

	// The later layer wins, and what only the earlier had survives. That is the
	// mount this replaces, expressed as a tree.
	for name, want := range map[string]string{
		overridden: "from the newer",
		"keep.txt": "only here",
	} {
		b, err := os.ReadFile(filepath.Join(at, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if string(b) != want {
			t.Errorf("%s holds %q, and the stack says %q", name, b, want)
		}
	}
}

// A squash into a store the guest has not got is refused.
func TestASquashWithNoStoreIsRefused(t *testing.T) {
	t.Parallel()

	c := pairWith(t, &guest.Server{})

	err := c.Squash(context.Background(), ir.NodeID{1}, []ir.NodeID{{2}})
	if err == nil {
		t.Fatal("a guest with no layer directory accepted a squash")
	}
}

// write puts a layer in a store.
func write(t *testing.T, root string, id ir.NodeID, files map[string]string) {
	t.Helper()

	at := store.LayerStore(root).Path(id)

	err := os.MkdirAll(at, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	for name, content := range files {
		err = os.WriteFile(filepath.Join(at, name), []byte(content), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
}
