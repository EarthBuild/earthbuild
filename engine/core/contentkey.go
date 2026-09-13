package core

import "github.com/EarthBuild/earthbuild/engine/ir"

// domainContent separates Κ_c from every other key derived here.
//
// 0x03 is the squash domain and 0x06 is the next free byte. Both derivations
// hash the same operation, environment and platform, so without this a content
// key and a chain key over a base whose content id equalled its own id would be
// the same bytes - and an entry published under one would be served under the
// other.
const domainContent = 0x06

// ContentSource is a 𝔅 that can say a layer's identity with times excluded.
//
// Optional, and asked of the store rather than required of it, exactly as
// PlacementSource is asked of a handle: a store with no manifest beside a layer
// - an older one, a base that arrived as opaque bytes - answers no and the
// caller falls back to the key it already had.
type ContentSource interface {
	ContentOf(id ir.NodeID) (ir.NodeID, bool)
}

// contentOf is what a store can say about a layer without its times, or nothing.
func contentOf(b BlobStore, id ir.NodeID) (ir.NodeID, bool) {
	source, ok := b.(ContentSource)
	if !ok {
		return ir.NodeID{}, false
	}

	return source.ContentOf(id)
}

// DeriveContentKey is Κ_c: the chain key with the clock taken out of the base.
//
// **A layer's identity hashes its mtimes (I8)**, so one deterministic step
// built twice produces two ids - creating a directory stamps it with the wall
// clock - and Κ₁, which names the base by those ids, misses. Measured on two
// independent cold builds of examples/rust-layered into separate stores:
// fourteen of the eighteen results carrying a delta agreed about their content
// and disagreed about their id. That is every eviction, on every machine, for
// as long as a base is ever rebuilt rather than pulled.
//
// Κ_c names the base by what it *holds* instead. Everything else is Κ₁'s: the
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

	content := make([]ir.NodeID, 0, len(base))

	for _, id := range base {
		c, ok := contentOf(blobs, id)
		if !ok {
			return Key{}, false
		}

		content = append(content, c)
	}

	h := ir.NewHasher()

	h.Byte(domainContent)
	// The generation, for cacheEpoch's reason: a wrong entry published here
	// reaches Κ₁ by promotion, and only an epoch can retire it.
	h.Count(cacheEpoch)

	// The base, by what it holds rather than by how it was made.
	h.Count(len(content))

	for _, c := range content {
		h.Fixed(c[:])
	}

	hashOperation(h, n, refs)
	hashEnvAndPlatform(h, n)

	return h.Sum(), true
}
