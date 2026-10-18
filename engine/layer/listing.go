package layer

import (
	"os"
	"sort"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ListingDigestOf is the digest of a directory's contents by name: green paper
// 𝐷, the value Κ₂ keys a directory on.
//
// **Names only, and deliberately.** What a step learns by enumerating a
// directory is which entries are in it; their contents it learns by reading
// them, which is 𝑅's business. A listing that hashed contents would make every
// edit anywhere below a directory invalidate every step that merely listed it.
//
// One function because two sides derive this value and they must agree
// byte-for-byte: the guest records it from the mount a step ran over, and the
// store recomputes it from a layer stack to check the recorded one still holds.
// Written out twice, they would be one rule maintained once - which is the shape
// of divergence this engine keeps finding, so the shape is removed rather than
// documented.
//
// Sorted here rather than by the caller. A directory listing has no order, and
// two machines that walked one directory differently must not key a build two
// ways.
func ListingDigestOf(names []string) ir.NodeID {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)

	h := ir.NewHasher()
	h.Count(len(sorted))

	for _, n := range sorted {
		h.Str(n)
	}

	return h.Sum()
}

// ListingDigestAt is ListingDigestOf for a directory on this filesystem.
//
// The merged view a step ran over, so the names are the ones the step would
// have seen: an overlay has already resolved the whiteouts that the layer-stack
// side has to resolve for itself.
func ListingDigestAt(dir string) (ir.NodeID, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ir.NodeID{}, err
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return ListingDigestOf(names), nil
}
