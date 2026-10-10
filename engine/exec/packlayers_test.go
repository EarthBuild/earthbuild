package exec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The host packs a stack carrying a declaration.
//
// E749 fixed this in the guest's packer and E751 in the squasher; this is the
// third consumer, and it had no existence check at all - it turned every stack
// element into a path and handed them to the archive writer. A declaration is
// filed as `layers/<id>.decl`, so its element became a path to nothing and the
// build failed much later with `lstat …: no such file or directory`, naming a
// layer that was never one (E761).
func TestTheHostPacksAStackCarryingADeclaration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	layer := ir.NodeID{4}

	err := os.MkdirAll(filepath.Join(root, "layers", layer.String()), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	declares, err := decl.Write(root, decl.Declaration{Env: []string{"PATH=/usr/bin"}})
	if err != nil {
		t.Fatal(err)
	}

	got, err := layerSources(root, []ir.NodeID{layer, declares})
	if err != nil {
		t.Fatalf("a stack carrying a declaration was refused: %v", err)
	}

	if len(got) != 1 {
		t.Errorf("the archive got %d layers, want the 1 that is a layer", len(got))
	}
}

// A layer the store has not got is refused here, and named.
//
// Without a check the archive writer discovers it, and says so as an `lstat` of
// a path - which names the store's layout and not the build. Refused here, the
// message can say what it is.
func TestTheHostRefusesAStackNamingALayerItHasNot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	layer := ir.NodeID{4}

	err := os.MkdirAll(filepath.Join(root, "layers", layer.String()), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	_, err = layerSources(root, []ir.NodeID{layer, {9}})
	if err == nil {
		t.Fatal("a stack naming a layer the store does not hold was packed")
	}

	if !strings.Contains(err.Error(), "holds no layer") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}
