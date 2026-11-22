package cli

import (
	"context"
	"io"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A stack holds declarations as well as trees, and only the trees are layers.
//
// **Which is which cannot be settled by looking in the host's store.** A
// microVM keeps its store on a device the host has no access to, so asking the
// host store answers "not here" for every element - and an image written from
// none of them has no filesystem at all. `docker run` on one cannot find `ls`.
// Measured: the same target wrote 2 layers under the namespace backend and 0
// under the microVM, which became the default without anything noticing (E-save).
//
// The scheduler sees `Declares` on every result it finishes, cached or run, so
// it is the one party that knows the answer without asking a store anything.
func TestOnlyTheTreesOfAStackBecomeLayers(t *testing.T) {
	t.Parallel()

	tree, declaration := idOf(t, 1), idOf(t, 2)

	packed := []ir.NodeID{}
	packer := func(_ context.Context, id ir.NodeID, _ io.Writer) error {
		packed = append(packed, id)

		return nil
	}

	sources := treeSources(
		context.Background(),
		[]ir.NodeID{tree, declaration},
		func(id ir.NodeID) bool { return id == declaration },
		packer,
	)

	if len(sources) != 1 {
		t.Fatalf("a two-element stack gave %d layers, wanted the one tree", len(sources))
	}

	err := sources[0](io.Discard)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}

	if len(packed) != 1 || packed[0] != tree {
		t.Errorf("packed %v, wanted just the tree %v", packed, tree)
	}
}

// Nothing declared means nothing skipped: the ordinary case is a stack that is
// all trees, and a predicate that never fires must not cost a layer.
func TestAStackOfTreesKeepsEveryOneOfThem(t *testing.T) {
	t.Parallel()

	stack := []ir.NodeID{idOf(t, 1), idOf(t, 2), idOf(t, 3)}

	sources := treeSources(
		context.Background(), stack,
		func(ir.NodeID) bool { return false },
		func(context.Context, ir.NodeID, io.Writer) error { return nil },
	)

	if len(sources) != len(stack) {
		t.Errorf("kept %d of %d", len(sources), len(stack))
	}
}

func idOf(t *testing.T, b byte) ir.NodeID {
	t.Helper()

	var id ir.NodeID
	id[0] = b

	return id
}
