package store

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// The deletion markers a committed layer carries, matching the guest's
// convention (`engine/guest/whiteout.go`) and OCI's.
//
// Named here rather than imported because the guest package is the *writer* and
// this is a reader on the other side of a process boundary: a reader importing
// the writer's unexported constants would not compile, and one that guessed
// them would drift. They are a wire format, and this is the second party to it.
const (
	whPrefix = ".wh."
	whOpaque = ".wh..wh..opq"
)

// View answers questions about a stack by reading the layers directly.
//
// `ViewSource` has been declared since S1 and implemented by nothing outside a
// test fake, so the whole L2 path - profiles, `Consistent`, Κ₂ - had never once
// run against a real filesystem. Flattening was in that state until E49 and was
// wrong when it finally ran.
//
// No mount, deliberately, and the cost is the contract: verifying a prediction
// must touch only the paths the prediction names. A view that materialised the
// stack would cost the mount L2 exists to avoid, and L2 would be slower than
// the rebuild it replaces.
func (s LayerStore) View(_ context.Context, stack []ir.NodeID) (core.BaseView, error) {
	roots := make([]string, 0, len(stack))
	for _, id := range stack {
		roots = append(roots, filepath.Join(string(s), "layers", id.String()))
	}

	return stackView{roots: roots}, nil
}

// SeenAsRoot reads this store the way a sandbox that shares it as root does.
//
// Κ₂ compares what a step observed with what a rebuilt step would see, and both
// happen inside the sandbox. Where the store is shared into a VM with everything
// owned by root, the guest digests uid 0 for a file the store holds as the
// invoking user - a constant offset that made every base look changed and the
// tier unable to serve a single RUN on darwin (E494).
//
// The guest cannot correct it: the shift is done by the sharing mechanism rather
// than a user namespace, so `/proc/self/uid_map` is the identity and there is
// nothing there to read. **The host knows, because the host is what shares it.**
//
// Only this view moves. A layer's own identity is hashed elsewhere and with the
// store's ownership, which is right: that is a fact about what was stored, and
// this is a question about what a step saw.
func (s LayerStore) SeenAsRoot(uid, gid uint32) core.ViewSource {
	return sharedAsRoot{store: s, uid: uid, gid: gid}
}

// sharedAsRoot is a LayerStore whose views read ownership as a guest does.
type sharedAsRoot struct {
	store    LayerStore
	uid, gid uint32
}

func (r sharedAsRoot) View(ctx context.Context, stack []ir.NodeID) (core.BaseView, error) {
	v, err := r.store.View(ctx, stack)
	if err != nil {
		return nil, err
	}

	sv, ok := v.(stackView)
	if !ok {
		return v, nil
	}

	sv.uids = layer.OneID(r.uid, 0)
	sv.gids = layer.OneID(r.gid, 0)

	return sv, nil
}

// stackView reads a layer stack as the merged filesystem it would mount as.
//
// roots are oldest first, matching the scheduler's stacks. uids and gids are how
// the *sandbox* presents the store's ownership, empty where it presents it
// unchanged - see SeenAsRoot.
type stackView struct {
	roots      []string
	uids, gids layer.IDMap
}

// Digest returns what is effectively at a path, and whether anything is.
//
// Newest layer first, and **a deletion marker means absent** rather than "keep
// looking". Getting that backwards is the failure this type exists to avoid: a
// step that observed a path missing would verify against a base still holding
// it, and L2 would serve a result computed without a file the rebuild would
// have seen (I3).
func (v stackView) Digest(path string) (ir.NodeID, bool) {
	rel := relative(path)

	name := filepath.Base(rel)

	// The markers themselves are not paths in the merged view. Asking for one
	// by name must not answer with the marker file.
	if name == whOpaque || strings.HasPrefix(name, whPrefix) {
		return ir.NodeID{}, false
	}

	for i := range slices.Backward(v.roots) {
		root := v.roots[i]

		if deleted(root, rel) {
			return ir.NodeID{}, false
		}

		d, err := layer.PathDigestIn(filepath.Join(root, rel), v.uids, v.gids)
		if err == nil {
			return d, true
		}
	}

	return ir.NodeID{}, false
}

// ListingDigest hashes a directory's merged names.
//
// The names alone, not what is at them: §3.4 says 𝐷 subsumes 𝑁 *within a listed
// directory* - "if the listing digest is unchanged, every absent path in it is
// still absent" - and that is a claim about which names exist. Hashing contents
// too would make a listing change whenever any file in it was edited, which is
// a correct-but-useless digest: it would never match across the base bumps L2
// exists to survive, and the entries that were read are already in 𝑅.
func (v stackView) ListingDigest(dir string) (ir.NodeID, bool) {
	rel := relative(dir)

	names := map[string]bool{}
	found := false

	// Oldest first, so a higher layer's deletions and additions apply over what
	// is underneath - the same order a mount resolves in.
	for _, root := range v.roots {
		p := filepath.Join(root, rel)

		entries, err := os.ReadDir(p)
		if err != nil {
			continue
		}

		found = true

		// An opaque marker means this layer replaces the directory below rather
		// than merging with it.
		for _, e := range entries {
			if e.Name() == whOpaque {
				names = map[string]bool{}

				break
			}
		}

		for _, e := range entries {
			n := e.Name()

			switch {
			case n == whOpaque:
			case strings.HasPrefix(n, whPrefix):
				delete(names, strings.TrimPrefix(n, whPrefix))
			default:
				names[n] = true
			}
		}
	}

	if !found {
		return ir.NodeID{}, false
	}

	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}

	// The same function the guest records with, so the recorded digest and the
	// one checked against it cannot drift apart. See layer.ListingDigestOf.
	return layer.ListingDigestOf(sorted), true
}

// deleted reports whether this layer carries a marker hiding rel.
func deleted(root, rel string) bool {
	marker := filepath.Join(root, filepath.Dir(rel), whPrefix+filepath.Base(rel))

	_, err := os.Lstat(marker)
	if err == nil {
		return true
	}

	// An opaque directory hides everything below it, including paths further
	// down than its immediate children.
	for d := filepath.Dir(rel); d != "." && d != string(filepath.Separator); d = filepath.Dir(d) {
		_, err := os.Lstat(filepath.Join(root, d, whOpaque))
		if err == nil {
			// Only when this layer does not itself provide the path.
			_, err = os.Lstat(filepath.Join(root, rel))

			return err != nil
		}
	}

	return false
}

// relative turns an absolute path in the merged view into one under a layer root.
//
// **Nothing can escape, and the containment is the `Clean`, not a refusal.** The
// doc here used to promise that an escaping path was refused, and the second
// return value was how it said so - except that it was `true` on every path
// through the function (unparam), because `filepath.Clean("/" + p)` resolves
// `..` against the root it just prefixed. `../../etc/passwd` becomes
// `etc/passwd`: contained, and not rejected.
//
// So the promise was kept by the normalisation and checked by nobody, and a
// caller reading the signature would have believed a guard existed. The bool is
// gone and TestAnEscapingPathIsContained asserts what actually holds.
func relative(p string) string {
	clean := filepath.Clean("/" + p)

	rel := strings.TrimPrefix(clean, "/")
	if rel == "" || rel == "." {
		return "."
	}

	return rel
}
