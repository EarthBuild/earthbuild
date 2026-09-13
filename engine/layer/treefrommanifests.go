package layer

import (
	"path"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// whOpaque marks a directory as holding nothing it inherited.
//
// The spelling store/view.go already reads. Kept here too rather than shared,
// because that one is about a mounted stack of directories and this is about
// manifests, and a constant imported across that boundary would suggest the two
// implementations must move together when what must agree is their *semantics*.
const whOpaque = ".wh..wh..opq"

// TreeFromManifests folds a stack into the tree it materialises, and digests it.
//
// **`contents(𝑏)` is a sequence; this is not.** Κₜ (green paper 4.5a) names a
// base by its layers' content ids in order, so two stacks that differ in how
// they were assembled are different keys however identical the filesystem they
// produce: a flattened stack and the one Φ (4.8) flattened, two branches that
// converge, independent steps written in either order. Folding to the tree makes
// those agree.
//
// Oldest first, as a stack is applied. The semantics are store/view.go's, which
// answers the same questions of a mounted stack: a later layer wins, `.wh.name`
// removes a name, and `.wh..wh..opq` removes everything a directory inherited
// while leaving what its own layer puts back. Those two implementations must
// agree, and only a test that materialises a stack and compares can say they do
// - which is why nothing keys on this yet.
//
// The digest is TakeIn's fold over the merged set, so a stack of one layer
// digests to that layer's own content id and the two tiers cannot disagree
// about a base that never needed merging.
func TreeFromManifests(ms [][]byte) ir.NodeID {
	merged := map[string]entry{}

	for _, m := range ms {
		entries, err := decodeManifest(m)
		if err != nil {
			// A manifest that cannot be read leaves the fold unable to say what
			// the stack holds. The zero digest is not an answer and callers
			// must not key on one; this returns it so that a caller comparing
			// two of them cannot accidentally find them equal to a real tree.
			return ir.NodeID{}
		}

		apply(merged, entries)
	}

	paths := make([]string, 0, len(merged))
	for p := range merged {
		paths = append(paths, p)
	}

	sort.Strings(paths)

	h := ir.NewHasher()
	h.Count(len(paths))

	for _, p := range paths {
		e := merged[p]
		e.hash(&h.Encoder, withoutTimes)
	}

	return h.Sum()
}

// apply lays one layer over the merged set.
func apply(merged map[string]entry, entries []entry) {
	// **Opaque first, and over the whole layer.** A directory marked opaque
	// holds nothing it inherited, but it does hold what its own layer puts in
	// it - so every marker is honoured before any of this layer's entries are
	// laid down, or a marker appearing after a sibling in walk order would
	// delete what the same layer had just written.
	for _, e := range entries {
		if path.Base(e.path) == whOpaque {
			clear(merged, path.Dir(e.path))
		}
	}

	for _, e := range entries {
		base := path.Base(e.path)

		switch {
		case base == whOpaque:
			// Handled above, and never a path in the merged view.

		case strings.HasPrefix(base, whPrefix):
			// A deletion, of a name and of everything under it: whiting out a
			// directory removes the directory, not merely its own entry.
			gone := path.Join(path.Dir(e.path), strings.TrimPrefix(base, whPrefix))

			delete(merged, gone)
			clear(merged, gone)

		default:
			merged[e.path] = e
		}
	}
}

// clear removes everything beneath a directory, leaving the directory itself.
func clear(merged map[string]entry, dir string) {
	prefix := dir + "/"
	if dir == "." || dir == "/" {
		prefix = ""
	}

	for p := range merged {
		if prefix != "" && strings.HasPrefix(p, prefix) {
			delete(merged, p)
		} else if prefix == "" && p != "." {
			delete(merged, p)
		}
	}
}
