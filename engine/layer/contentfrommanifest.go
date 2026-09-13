package layer

import "github.com/EarthBuild/earthbuild/engine/ir"

// ContentFromManifest is a layer's identity with times excluded, read from the
// manifest kept beside it.
//
// **The identity a key can survive a rebuild on.** `Capture.ID` hashes mtimes
// (I8), so one deterministic step built twice produces two ids - `mkdir` stamps
// a directory with the wall clock - and every key derived from them misses. The
// measurement is written down beside `Entry.Content`: two cold builds of this
// repository's Rust example agreed about the content of fourteen of the
// eighteen results that have one, and disagreed about the id of all fourteen.
//
// `Content` is that digest and is already computed at capture. What it has
// never had is a way to be asked of a *base*, which arrives as layer ids and
// not as a capture. The manifest is that way: it is "the bytes the digest is
// already over" (see NoteManifest), so this is a decode and a fold rather than
// the walk it replaces - 463 ms and 898 MB of reads on this repository's own
// Rust base layer, per NoteManifest's own measurement.
//
// The fold must match TakeIn's exactly - the count, then each entry
// `withoutTimes` - or a base named from its manifest is not the base the walk
// named. TestAManifestYieldsTheSameContentIDAsTheWalk is that assertion, and it
// is asserted rather than assumed for the reason the manifest round trip
// already is.
//
// Ownership is not translated here as TakeIn translates it. A manifest is
// written by `ManifestIn` with the maps already applied, so the numbers in it
// are the ones the store would produce; applying them twice would move them.
func ContentFromManifest(m []byte) (ir.NodeID, error) {
	entries, err := decodeManifest(m)
	if err != nil {
		return ir.NodeID{}, err
	}

	h := ir.NewHasher()
	h.Count(len(entries))

	for _, e := range entries {
		e.hash(&h.Encoder, withoutTimes)
	}

	return h.Sum(), nil
}
