package store

import (
	"bytes"
	"fmt"
	"sort"

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

	_, size, err := d.tree(layerID, content)

	return size, err
}

// TreeMessage is the REAPI `Tree` a layer materialises to: its root Directory
// with every directory beneath it inline, kept in this store under its own
// name.
//
// **For a client that reads `tree_digest` and nothing else.** The nodes say the
// same thing by reference and are what this engine uses; Buck2 refuses a result
// without a Tree, so one is written for it. Filed here rather than returned
// alone because the client fetches it by digest a moment later.
func (d DirStore) TreeMessage(layerID, content ir.NodeID) (ir.NodeID, int64, error) {
	t, _, err := d.tree(layerID, content)
	if err != nil {
		return ir.NodeID{}, 0, err
	}

	nodes := t.Nodes()

	children := make([][]byte, 0, len(nodes))

	for id, b := range nodes {
		if id != t.Root() {
			children = append(children, b)
		}
	}

	// **Sorted, because a map is not.** Two runs producing the same tree must
	// produce the same Tree message, or its digest is a different name for one
	// filesystem on every build (green paper I1).
	sort.Slice(children, func(i, j int) bool { return bytes.Compare(children[i], children[j]) < 0 })

	msg := layer.EncodeTree(nodes[t.Root()], children)

	id := ir.DigestOf(msg)
	if err := d.Accept(id, msg); err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("keep the tree message: %w", err)
	}

	return id, int64(len(msg)), nil
}

// tree folds a layer's manifest and checks it against what the capture said.
func (d DirStore) tree(layerID, content ir.NodeID) (layer.Tree, int64, error) {
	m, ok, err := ReadManifest(string(d), layerID)
	if err != nil || !ok {
		return layer.Tree{}, 0, fmt.Errorf("no manifest for the layer under %s", layerID)
	}

	f := layer.NewFold()
	if !f.Add(m) {
		return layer.Tree{}, 0, fmt.Errorf("the manifest for %s could not be folded", layerID)
	}

	tree := f.Tree()

	if tree.Root() != content {
		return layer.Tree{}, 0, fmt.Errorf(
			"the layer under %s folds to %s and the entry says %s",
			layerID, tree.Root(), content)
	}

	// Best effort: a store that could not keep the nodes has still computed
	// them, and the caller's answer does not depend on their being kept.
	_ = d.NoteNodes(tree)

	return tree, int64(len(tree.Nodes()[tree.Root()])), nil
}
