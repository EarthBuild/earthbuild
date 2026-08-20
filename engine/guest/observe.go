package guest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// watcher accumulates what a step looked at in its base.
//
// Held by the server against a handle rather than by the handle itself, because
// a handle is a materialised filesystem and this is a record of what somebody
// did with one - and because core.Handle is implemented by four types, three of
// which have nothing to observe.
type watcher struct {
	mu       sync.Mutex
	reads    map[string]ir.NodeID
	negative []string
	why      map[string]bool
}

func (w *watcher) read(path string, id ir.NodeID) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.reads == nil {
		w.reads = map[string]ir.NodeID{}
	}

	w.reads[path] = id
}

func (w *watcher) absent(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.negative = append(w.negative, path)
}

// lose marks the observation as knowingly incomplete.
//
// The field exists so that a lossy source is *usable*: loss that is declared
// costs an L2 hit, loss that is hidden costs correctness (green paper §3.4).
func (w *watcher) lose(why ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.why == nil {
		w.why = map[string]bool{}
	}

	// A gap with no reason given still counts. Most callers here know only that
	// they could not tell - a permission failure on a path component, a symlink
	// they cannot follow - and saying so is better than a reason invented to
	// fill the field.
	if len(why) == 0 {
		w.why[whyUnstated] = true

		return
	}

	for _, r := range why {
		w.why[r] = true
	}
}

// whyUnstated is a gap whose cause the caller did not name.
const whyUnstated = "the guest could not tell what a step looked at"

// observation is what this watcher saw, as green paper's 𝑟.
func (w *watcher) observation() core.Observation {
	w.mu.Lock()
	defer w.mu.Unlock()

	obs := core.Observation{
		Reads:      make(map[string]ir.NodeID, len(w.reads)),
		Listings:   map[string]ir.NodeID{},
		Negative:   append([]string(nil), w.negative...),
		Incomplete: len(w.why) > 0,
		Why:        make([]string, 0, len(w.why)),
	}

	for r := range w.why {
		obs.Why = append(obs.Why, r)
	}

	// Sorted, because it is read out of a map and a map's order is not one
	// (I12).
	slices.Sort(obs.Why)

	for p, id := range w.reads {
		obs.Reads[p] = id
	}

	return obs
}

// watcherFor returns the record for a handle, making one on first use.
func (s *Server) watcherFor(h core.Handle) *watcher {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()

	if s.obs == nil {
		s.obs = map[core.Handle]*watcher{}
	}

	w, ok := s.obs[h]
	if !ok {
		w = &watcher{}
		s.obs[h] = w
	}

	return w
}

// observationOf is what has been recorded against a handle.
func (s *Server) observationOf(h core.Handle) core.Observation {
	return s.watcherFor(h).observation()
}

// observeDest records what a copy looked at to decide where its source lands.
//
// **What is looked at is the destination's kind, and only that.** `COPY x /app/`
// places inside a directory and renames onto anything else; `COPY --dir tree
// /placed` gives /placed/tree when /placed exists and /placed itself when it
// does not. Contents never enter it - what the step produces is the delta it
// writes, which is the same whatever was underneath - so the digest here is the
// path's own entry, mode included, which is what tells a directory from a file.
//
// The whole chain, not just the leaf. The leaf's kind decides where the source
// lands; an ancestor decides whether the copy succeeds at all, because an `/a`
// that is a *file* makes `COPY x /a/b` fail rather than land elsewhere, and a
// prediction ignoring the ancestors would be reused against a base where the
// real build errors. Bounded by the components of a path somebody wrote in an
// Earthfile.
//
// An absent path is a *negative* lookup rather than an omission. The copy
// behaved as it did **because** nothing was there, and a base where something is
// would produce a different layer: 𝑁 is not a refinement of 𝑅 (§3.4, I3), and a
// source recording only reads admits exactly that false hit.
//
// `rel` is the path as the step names it, so a prediction is checkable against
// Views.View - which is built from the base stack and knows nothing of the
// guest's overlay paths.
func (s *Server) observeDest(h core.Handle, abs, rel string) {
	w := s.watcherFor(h)

	// The destination as the *step* names it, whatever it arrived as.
	//
	// One profile carried 125 negative lookups spelled
	// `/var/lib/earthbuild/scratch/mounts/h-3790805740/merged/app/package.json`
	// - a path with a handle id in it, which can match nothing on a later build
	// and is not what the base holds the file under. The same normalisation the
	// traced reads get (E497, E498), at the same boundary, because this is the
	// other place an observation is recorded.
	if root := h.Root(); root != "" {
		inside, ok := insideRoot(rel, root, filepath.Dir(filepath.Dir(root)))
		if !ok {
			return
		}

		rel = inside
	}

	// Stopping *above* the root, deliberately. `/` is the one component whose
	// existence is never in question - a copy's destination decides where its
	// source lands, and "the filesystem has a root" decides nothing - while its
	// digest carries mode, ownership and extended attributes that differ
	// between two base images for reasons no copy depends on.
	//
	// Including it made every copy's prediction stale the moment the base
	// moved, which is exactly the case the tier exists for: measured on a bump
	// from alpine:3.21 to alpine:3.22, `1 of 3 predictions stale` and the copy
	// rebuilt (E125).
	for a, r := abs, cleanSlash(rel); r != "/"; a, r = filepath.Dir(a), parentOf(r) {
		// Through this process's own mapping, so the number is the one the
		// store would produce rather than the one this namespace sees: a
		// directory the guest made is uid 0 here and the invoking user there,
		// and the view is computed on the other side (E133).
		uids, gids := OwnIDMaps()

		id, err := layer.PathDigestIn(a, uids, gids)

		switch {
		case err == nil:
			w.read(r, id)

			// A symlink among the components is a gap this cannot close. The
			// digest tells a link from a directory, so two bases differing
			// *there* are caught; where the link points is not recorded, and
			// two bases whose targets differ would satisfy one prediction.
			// Declared rather than ignored - see watcher.lose.
			fi, statErr := os.Lstat(a)
			if statErr == nil && fi.Mode()&fs.ModeSymlink != 0 {
				w.lose()
			}

		// errors.Is, not os.IsNotExist: the latter predates error wrapping and
		// answers false for a wrapped error, and layer.PathDigest wraps its
		// stat. With os.IsNotExist an absent destination silently became
		// "cannot tell" - the safe direction, still wrong, and hidden by the
		// destination-exists case passing.
		case errors.Is(err, fs.ErrNotExist):
			w.absent(r)

		default:
			// Neither a read nor an absence: a permission failure says nothing
			// about what is there, and recording it as either would be a claim
			// the guest cannot make. Declared lossy instead, because a gap that
			// is not declared is the false hit I3 exists to prevent.
			w.lose()
		}
	}
}

// cleanSlash is a destination as the step names it: absolute, slash-separated.
func cleanSlash(rel string) string {
	return filepath.ToSlash(filepath.Clean("/" + strings.TrimPrefix(rel, "/")))
}

// parentOf is the containing directory, stopping at the root.
func parentOf(p string) string {
	d := filepath.ToSlash(filepath.Dir(p))
	if d == "." {
		return "/"
	}

	return d
}

// merge combines two observations of one step.
//
// Union, and **incomplete is sticky**: a whole made of a complete half and a
// lossy half is lossy. Getting that the other way round would be a source that
// launders its own gaps by being averaged with one that has none, which is the
// false hit `Incomplete` exists to prevent.
func merge(a, b core.Observation) core.Observation {
	out := core.Observation{
		Reads:      map[string]ir.NodeID{},
		Listings:   map[string]ir.NodeID{},
		Negative:   append(append([]string(nil), a.Negative...), b.Negative...),
		Incomplete: a.Incomplete || b.Incomplete,
		Why:        mergeWhy(a.Why, b.Why),
	}

	for _, src := range []core.Observation{a, b} {
		for p, id := range src.Reads {
			out.Reads[p] = id
		}

		for p, id := range src.Listings {
			out.Listings[p] = id
		}
	}

	return out
}

// mergeWhy is the union of two observations' reasons, sorted and each once.
func mergeWhy(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}

	seen := map[string]bool{}
	for _, w := range append(append([]string(nil), a...), b...) {
		seen[w] = true
	}

	out := make([]string, 0, len(seen))
	for w := range seen {
		out = append(out, w)
	}

	slices.Sort(out)

	return out
}
