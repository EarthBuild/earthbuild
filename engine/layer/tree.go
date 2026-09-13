package layer

import (
	"bytes"
	"path"
	"slices"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// domainTreeNode separates a directory node from every key in the engine.
//
// A node digest and a cache key are both ℋ over an encoding, and §3.1 gives ℋ
// no algorithm identifier - so without a domain byte a node could be produced
// that collides with Κ₁, Κₜ or Κ₂. See green paper §4.4.
const domainTreeNode = 0x07

// Tree is a merged set as a Merkle tree of directories.
//
// **A node names what is under it and nothing about where it is.** Two bases
// holding the same vendor directory hold the same node, whatever surrounds it,
// which is what lets a peer say "I have that subtree" without being told its
// context - and what lets a worker fetch only the directories it touches.
//
// Every node's digest is ℋ over exactly the bytes Nodes hands back, so a
// receiver verifies a blob against the name it asked for rather than trusting
// the sender (I4's reason, applied to transport).
type Tree struct {
	root  ir.NodeID
	nodes map[ir.NodeID][]byte
}

// Root is 𝜏, the digest Κₜ (green paper 4.5a) keys on.
func (t Tree) Root() ir.NodeID { return t.root }

// Nodes is every directory in the tree, by digest.
//
// Includes the root. A tree with no directories below it is one node, which is
// the root itself - a flat layer is still addressable.
func (t Tree) Nodes() map[ir.NodeID][]byte { return t.nodes }

// dir is a directory being assembled from paths.
type dir struct {
	// own is the directory's own entry, where the layer recorded one. A tar
	// need not carry an entry for a directory it only implies, so a node says
	// whether it had one rather than inventing default metadata - which would
	// make two layers agree that differed.
	own     *entry
	files   map[string]entry
	subdirs map[string]*dir

	// digest is what this directory was last named, and clean whether that is
	// still true. A fold carried across Add dirties only the path it moved and
	// the directories above it, so an untouched subtree is never named twice.
	digest ir.NodeID
	clean  bool
}

func newDir() *dir {
	return &dir{files: map[string]entry{}, subdirs: map[string]*dir{}}
}

// treeOf builds the Merkle tree of a merged set.
//
// One function, because a second place that digested a tree would be a second
// definition of what a base *is*, and the two would agree until somebody edited
// one. capture and Fold.Digest both come here.
func treeOf(merged map[string]entry) Tree {
	b := builder{nodes: map[ir.NodeID][]byte{}}
	t := Tree{nodes: b.nodes}
	t.root = b.digest(rootOf(merged))

	return t
}

// rootDigestOf is 𝜏 without keeping a blob for every directory.
//
// **The key needs the name, not the bytes.** Κₜ is consulted once per step and
// retains nothing; shipping subtrees is a different job with a different cost,
// so only Tree pays to hold 65MB of node encodings for a 20k-entry base.
func rootDigestOf(merged map[string]entry) ir.NodeID {
	b := builder{}

	return b.digest(rootOf(merged))
}

// rootOf assembles the directory trie of a merged set.
func rootOf(merged map[string]entry) *dir {
	root := newDir()

	for p, e := range merged {
		at := root

		parts := strings.Split(path.Clean(p), "/")
		for _, part := range parts[:len(parts)-1] {
			next, ok := at.subdirs[part]
			if !ok {
				next = newDir()
				at.subdirs[part] = next
			}

			at = next
		}

		name := parts[len(parts)-1]

		if kindOf(e.mode) == 'd' {
			next, ok := at.subdirs[name]
			if !ok {
				next = newDir()
				at.subdirs[name] = next
			}

			own := e
			next.own = &own

			continue
		}

		at.files[name] = e
	}

	return root
}

// builder digests directories, optionally keeping each one's bytes.
//
// The scratch buffer is shared across nodes and so every child is digested
// before its parent is written - which is also the only order a Merkle tree
// admits. nodes nil means the encodings are hashed and dropped.
type builder struct {
	buf   bytes.Buffer
	nodes map[ir.NodeID][]byte
}

// digest writes one directory's encoding, names it, and keeps it where asked.
//
// **A directory's own metadata lives in its parent, not in its own node.** A
// node is then the contents alone, so a subtree keeps one name however its
// enclosing directory is permissioned - which is the reuse the tier is for. The
// root has no parent and so no metadata anywhere, which is correct: a stack's
// root is the mount point and not a thing the layers describe.
func (b *builder) digest(d *dir) ir.NodeID {
	return b.encode(d, func(child *dir) ir.NodeID { return b.digest(child) })
}

// encode writes one directory's encoding, names it, and keeps it where asked.
//
// The child digests come from the caller, which is the only difference between
// naming a whole tree and naming what a layer changed - the encoding itself is
// written once, here, so the two cannot drift.
func (b *builder) encode(d *dir, child func(*dir) ir.NodeID) ir.NodeID {
	subs := make([]string, 0, len(d.subdirs))
	for n := range d.subdirs {
		subs = append(subs, n)
	}

	slices.Sort(subs)

	// Children first: they share this buffer, so none of them may be building
	// while the parent is written.
	children := make([]ir.NodeID, len(subs))
	for i, n := range subs {
		children[i] = child(d.subdirs[n])
	}

	names := make([]string, 0, len(d.files))
	for n := range d.files {
		names = append(names, n)
	}

	slices.Sort(names)

	b.buf.Reset()

	enc := ir.NewEncoder(&b.buf)
	enc.Byte(domainTreeNode)
	enc.Count(len(names))

	for _, n := range names {
		e := d.files[n]
		e.hashAs(enc, n, withoutTimes)
	}

	enc.Count(len(subs))

	for i, n := range subs {
		enc.Str(n)
		enc.Bool(d.subdirs[n].own != nil)

		if own := d.subdirs[n].own; own != nil {
			own.hashAs(enc, n, withoutTimes)
		}

		enc.Fixed(children[i][:])
	}

	id := ir.DigestOf(b.buf.Bytes())

	if b.nodes != nil {
		b.nodes[id] = bytes.Clone(b.buf.Bytes())
	}

	return id
}

// insert places an entry at a path, dirtying every directory above it.
func (d *dir) insert(parts []string, e entry) {
	d.clean = false

	name := parts[0]

	if len(parts) > 1 {
		next, ok := d.subdirs[name]
		if !ok {
			next = newDir()
			d.subdirs[name] = next
		}

		next.insert(parts[1:], e)

		return
	}

	if kindOf(e.mode) == 'd' {
		next, ok := d.subdirs[name]
		if !ok {
			next = newDir()
			d.subdirs[name] = next
		}

		own := e
		next.own = &own
		next.clean = false

		// A name cannot be a file and a directory at once, and a later layer
		// replacing one with the other must not leave the old behind.
		delete(d.files, name)

		return
	}

	delete(d.subdirs, name)

	d.files[name] = e
}

// remove takes a path out, prunes what it empties, and dirties what is above.
//
// A directory that loses its own entry but keeps children stays: the fold's
// merged set can hold `a/b.txt` with nothing recorded for `a`, and the node then
// says it had no entry rather than inventing one.
func (d *dir) remove(parts []string) {
	d.clean = false

	name := parts[0]

	if len(parts) > 1 {
		next, ok := d.subdirs[name]
		if !ok {
			return
		}

		next.remove(parts[1:])

		if next.empty() {
			delete(d.subdirs, name)
		}

		return
	}

	delete(d.files, name)

	if next, ok := d.subdirs[name]; ok {
		next.own = nil
		next.clean = false

		if next.empty() {
			delete(d.subdirs, name)
		}
	}
}

// empty reports a directory nothing records and nothing lives under.
func (d *dir) empty() bool {
	return d.own == nil && len(d.files) == 0 && len(d.subdirs) == 0
}

// cached names a directory, reusing what is still true beneath it.
//
// **The node is the unit of reuse in both directions.** A subtree no layer
// touched keeps its name, which is what makes the digest incremental here and
// what lets a peer skip fetching it there - one property, read twice.
func (b *builder) cached(d *dir) ir.NodeID {
	if d.clean {
		return d.digest
	}

	d.digest = b.encode(d, func(child *dir) ir.NodeID { return b.cached(child) })
	d.clean = true

	return d.digest
}
