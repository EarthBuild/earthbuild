package core

import (
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// TreeSource is a 𝔅 that can say what a stack materialises to.
//
// **A stack rather than a layer, which is the whole change.** Asking per layer
// gave a *sequence* of content ids, and a sequence distinguishes two stacks that
// materialise one filesystem: the stack Φ (4.8) flattened and the stack it
// flattened, two branches that converge, independent steps written in either
// order. Asking what the stack holds does not.
//
// Optional, and asked of the store rather than required of it, exactly as
// PlacementSource is asked of a handle: a store that cannot fold a stack - a
// layer with no manifest beside it, a base that arrived as opaque bytes -
// answers no, and the caller falls back to the key it already had.
type TreeSource interface {
	TreeOf(stack []ir.NodeID) (ir.NodeID, bool)
}

// treeOf is what a store can say a stack materialises to, or nothing.
func treeOf(b BlobStore, stack []ir.NodeID) (ir.NodeID, bool) {
	source, ok := b.(TreeSource)
	if !ok {
		return ir.NodeID{}, false
	}

	return source.TreeOf(stack)
}

// DeriveContentKey is Κₜ, green paper (4.5a): the chain key with the clock
// taken out of the base.
//
// **A layer's identity hashes its mtimes (I8)**, so one deterministic step
// built twice produces two ids - creating a directory stamps it with the wall
// clock - and Κ₁, which names the base by those ids, misses. Measured on two
// independent cold builds of examples/rust-layered into separate stores:
// fourteen of the eighteen results carrying a delta agreed about their content
// and disagreed about their id. That is every eviction, on every machine, for
// as long as a base is ever rebuilt rather than pulled.
//
// Κₜ names the base by what it *holds* instead. Everything else is Κ₁'s: the
// same operation, environment and platform, hashed the same way, because the
// only thing wrong with Κ₁ here is the identity it uses for 𝑏.
//
// **Sound for Κ₁'s own reason.** Two bases with one content id materialise to
// one filesystem, and A3 already says a step over one filesystem produces one
// result. This is a coarser-invariant key over the same evidence, not a new
// kind of trust - unlike Κ₂, which requires believing a tracer saw everything.
//
// Not derivable is an ordinary answer, never a guess: a base with one unknown
// layer returns false and the caller is left with the key it had. Substituting
// the layer id for an unknown content would let two bases holding anything at
// all share a key.
//
// Images never arrive here. Their ids are content-addressed with no clock in
// them, so Κ₁ already matches across a rebuild - measured in the same pair of
// builds, where the seven entries whose ids agreed were exactly the seven with
// no content digest at all.
func DeriveContentKey(
	n *ir.Node, base, refs []ir.NodeID, blobs BlobStore,
) (Key, bool) {
	if blobs == nil {
		return Key{}, false
	}

	// **What the base holds, not how it was made.** A fold over the manifests
	// beside the stack's layers, which the store does because that is where
	// they are - and once per stack rather than once per layer, the answer
	// being about the stack.
	//
	// Not derivable is an ordinary answer and never a guess. Keying on the
	// stack's own ids where the fold is unavailable would be Κ₁ under another
	// domain: a second entry published for nothing, and a hit that told Κ₁
	// nothing it did not already know.
	tree, ok := treeOf(blobs, base)
	if !ok {
		return Key{}, false
	}

	// **Κₜ is the Action digest**, not a digest of our own beside one. Every
	// part of the key has a home in the message - the argv and environment in
	// the Command, the tree in input_root_digest, the generation in salt, and
	// everything this API has no field for in one platform property. So a key
	// this engine derives is the number another tool asks for, rather than a
	// translation of it (plan-remote-execution R2b).
	//
	// No domain byte: an Action begins with a protobuf tag, which cannot
	// collide with Κ₁'s or Κ₂'s leading 0x01 or 0x02 (green paper 4.5b).
	return Key(ir.DigestOf(layer.EncodeAction(ActionOf(n, refs, tree)))), true
}
