package store

import (
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// TreeNodes makes sure the tree a layer materialises to is in this store, and
// reports its root Directory's serialised length.
//
// **The nodes are written when somebody asks, and not when the layer is made.**
// Folding a manifest into a tree on the capture path cost 0.6ms against 54.3ms
// - ninety times - on every step, to produce nodes that most builds never
// fetch. So it happens here: the first caller that needs the tree pays for it,
// and every caller after that finds it already written.
//
// `content` is what the layer's own capture recorded, and the fold is checked
// against it rather than trusted. A fold that lands elsewhere means this store
// cannot produce the tree the entry describes, and serving a different one
// under the client's name is the one thing a content-addressed store may never
// do.
func (d DirStore) TreeNodes(layerID, content ir.NodeID) (int64, error) {
	// Already written, by an earlier caller or by the build that made it.
	if b, err := d.Node(content); err == nil {
		return int64(len(b)), nil
	}

	m, ok, err := ReadManifest(string(d), layerID)
	if err != nil || !ok {
		return 0, fmt.Errorf("no manifest for the layer under %s", layerID)
	}

	f := layer.NewFold()
	if !f.Add(m) {
		return 0, fmt.Errorf("the manifest for %s could not be folded", layerID)
	}

	tree := f.Tree()

	if tree.Root() != content {
		return 0, fmt.Errorf(
			"the layer under %s folds to %s and the entry says %s",
			layerID, tree.Root(), content)
	}

	// Best effort: a store that could not keep the nodes has still computed
	// them, and the caller's answer does not depend on their being kept.
	_ = d.NoteNodes(tree)

	return int64(len(tree.Nodes()[tree.Root()])), nil
}
