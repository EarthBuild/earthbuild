package layer

import (
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// PathDigest is what one path contributes to a layer's content identity.
//
// This is the value 𝑅 maps a path to (green paper §3.4) and the value
// `BaseView.Digest` returns, and it must be one function for both: a prediction
// recorded by one and checked by the other is a comparison between two
// questions if they are two functions. That is E113's shape, before it happens.
//
// Metadata is included, because §3.3 metadata decides what a step does with a
// file - the same bytes made executable are a different input to a step that
// runs them.
//
// **Times are not**, and that is the one deliberate omission. `Capture` already
// has both flavours: `ID` carries mtimes and `Content` does not. A view is built
// by materialising a stack, and two materialisations of one layer set the same
// bytes at different moments, so a digest carrying mtime would make every
// prediction inconsistent with every base - L2 would never hit while appearing
// to work, which is the failure mode that looks like the feature being
// worthless rather than broken.
func PathDigest(p string) (ir.NodeID, error) { return PathDigestIn(p, IDMap{}, IDMap{}) }

// PathDigestIn is PathDigest with ownership translated as a namespace sees it.
//
// The guest and the host read the same stored layer through different id
// mappings: a directory the guest created is uid 0 to the guest and the
// invoking user to the host. `PathDigest` hashes ownership deliberately, so an
// observation recorded on one side never matched a view computed on the other
// and every prediction about it went stale on the first base change (E132).
//
// One side has to translate and it is the guest's to do: it is the only party
// that knows its own mapping, it records once per step rather than once per
// lookup, and the host keeps no idea of what a namespace is. The zero map is
// the identity, which is what every other caller wants.
func PathDigestIn(p string, uids, gids IDMap) (ir.NodeID, error) {
	entries, _, err := walkOne(p)
	if err != nil {
		return ir.NodeID{}, err
	}

	h := ir.NewHasher()

	// One entry, and its own name is not hashed by the caller here: a path's
	// digest is about what is *at* the path, so that a file moved between
	// layers at the same path compares equal.
	for _, e := range entries {
		// Translated before hashing, so the number is the one the store would
		// produce - not the one this namespace happens to see.
		// Two maps, not one. uid and gid are separate mappings and the engine
		// writes both (E105); on the measured machine the invoking user is
		// 1000 and its group is 100, so translating a gid through the uid map
		// turns 0 into 1000 and the digests disagree by exactly the amount
		// that looks like nothing.
		//
		// The doc comment on the guest's reader said this before the code did
		// it, which is the session's own failure class committed while removing
		// it (E133).
		e.uid = uids.Outside(e.uid)
		e.gid = gids.Outside(e.gid)

		e.hash(&h.Encoder, withoutTimes)
	}

	return h.Sum(), nil
}
