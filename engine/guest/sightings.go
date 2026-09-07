package guest

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/trace"
)

// recordSightings turns what a step was seen to look at into an observation.
//
// The same shape as observeDest and for the same reasons. A path the step named
// is digested *in the mount* - so the value is the one the store would produce
// rather than the one this namespace sees - and a path that is not there is a
// negative lookup rather than an omission: the step behaved as it did **because**
// nothing was there, and a base where something is would build differently
// (§3.4, I3).
func (s *Server) recordSightings(
	h core.Handle, root string, seen trace.Sightings, provided []string,
	own func(string) bool,
) {
	w := s.watcherFor(h)

	if seen.Incomplete {
		// The tracer's own reasons, not a fresh one: it knows whether a call was
		// in another architecture's numbering or a path could not be read, and
		// that is the difference between a fixable gap and a permanent one.
		w.lose(seen.Why...)
	}

	uids, gids := OwnIDMaps()

	opened := make(map[string]bool, len(seen.Opened))
	for _, p := range seen.Opened {
		opened[p] = true
	}

	for _, p := range seen.Paths {
		// The name the tracer used, kept because `Opened` is keyed on it and p
		// is renamed below to the name the base holds. Checking the renamed one
		// would match nothing for exactly the paths that get renamed, which is
		// every path inside the step's own root - so the narrowing would look
		// like it worked and quietly record no listing at all.
		outside := p

		// Not the base's to describe. What this engine mounted into the step -
		// the resolver, /proc, /dev, a cache directory - is regenerated or
		// shared, so recording it makes the step stale on every later build
		// whatever it actually read (E222).
		if under(p, provided) {
			continue
		}

		// The tracer resolves a path as *it* sees it, outside the step's root,
		// so some arrive by their outside name:
		// `/var/lib/earthbuild/scratch/mounts/h-3452187907/merged/usr/lib/...`.
		//
		// Two different things wear that prefix, and they need opposite
		// treatment (E497, E498):
		//
		//   - the root itself is this engine's machinery and is not part of any
		//     base. Recorded as a negative it claims a path was absent from a
		//     base it does not belong to.
		//   - anything *under* it is a real file, named from outside. Dropping
		//     it loses a genuine input; keeping the outside name stores a path
		//     with a per-build id in it, which can match nothing on a later
		//     build. In a real profile that was half the entries: the same
		//     files twice, once each way.
		//
		// So the root is dropped and the rest is renamed to what the step calls
		// it - which is what the base holds it under, and the only name a later
		// build can compare.
		if root != "" {
			// The directory handles live in, derived from this one rather than
			// asked of the Server: `mountStore()` answers where the *host*
			// side puts them, which is not where this guest's materialiser
			// does - 125 entries went on being recorded while that was the
			// question being asked (E498).
			//
			// A root is `<mounts>/h-1234/merged`, so its grandparent is
			// `<mounts>`. Structural, and the structure is this package's own.
			inside, ok := insideRoot(p, root, filepath.Dir(filepath.Dir(root)))
			if !ok {
				continue
			}

			p = inside
		}

		// **A file the step made is not a file it read.** `printf > f && cat f`
		// is a real read of a path the base cannot hold, so recording it as an
		// input makes the prediction stale on every later build (E696). A path
		// the step *edited* is a different thing and is kept: the read was of
		// the base, and dropping it would be a false hit (I3).
		//
		// After the renaming above, because the question is about the name the
		// base would hold it under.
		if own != nil && own(p) {
			continue
		}

		abs := filepath.Join(root, filepath.Clean("/"+p))

		id, err := layer.PathDigestIn(abs, uids, gids)

		switch {
		case err == nil:
			w.read(p, id)

			// **A directory is also enumerated, and the read cannot say so.**
			// PathDigestIn digests the entry at the path - for a directory its
			// own mode and ownership - which is unchanged by a file appearing
			// inside it. So a step that lists rather than reads (`find`, `ls`,
			// a shell glob, every compiler that scans a source directory) was
			// keyed on nothing that moves when the directory's contents do, and
			// took an L2 hit against a base holding different files: I3, the
			// one failure this design exists to prevent.
			//
			// **Only a directory the step opened.** `getdents` needs a
			// descriptor and a descriptor needs an open, so an opened directory
			// is a sound over-approximation of an enumerated one - while a
			// directory merely stat'ed is one the step walked *past*, which
			// every step does to every ancestor of everything it reads.
			// Recording those too was the first version of this fix and it
			// re-ran `RUN cat /c/f.txt` whenever any sibling of `f.txt`
			// appeared. A tracer that lost the distinction would have to keep
			// the wide rule, since erring is only allowed in one direction; this
			// one keeps it.
			if opened[outside] {
				listing, listErr := layer.ListingDigestAt(abs)
				if listErr == nil {
					w.list(p, listing)
				}
			}

		case errors.Is(err, fs.ErrNotExist):
			w.absent(p)

		default:
			// Neither a read nor an absence. A permission failure says nothing
			// about what is there, and recording it as either would be a claim
			// the guest cannot make.
			w.lose()
		}
	}
}

// under reports whether a path is at or inside one of these mount points.
//
// On path components, not on characters: `/etc/resolv.conf.bak` is a file in the
// base and starts with `/etc/resolv.conf`. A prefix match would drop it, and the
// mistake is silent in the safe direction - a lost read is a miss - which is
// exactly why it would never be found.
func under(path string, points []string) bool {
	clean := filepath.Clean("/" + strings.TrimPrefix(path, "/"))

	for _, m := range points {
		at := filepath.Clean("/" + strings.TrimPrefix(m, "/"))

		if clean == at || strings.HasPrefix(clean, at+"/") {
			return true
		}
	}

	return false
}

// insideRoot renames a path the tracer saw from outside to what the step calls
// it, and reports whether it is worth recording at all.
//
// A path that is not under the root is already an inside path and is returned
// unchanged: the tracer reports both forms, depending on how the step named the
// file.
func insideRoot(p, root, mounts string) (string, bool) {
	clean := filepath.Clean(p)
	at := filepath.Clean(root)

	if clean == at {
		// The root itself: this engine's own directory, not a path in a base.
		return "", false
	}

	if rel, under := strings.CutPrefix(clean, at+string(filepath.Separator)); under {
		return filepath.Clean("/" + rel), true
	}

	// Under *another* handle's root, which is a different materialisation.
	//
	// Dropped rather than renamed. The digest is taken from this step's own
	// root, so renaming would record what *this* base holds at a path the step
	// read somewhere else - and if the two differ that is a hit the rebuild
	// would not reproduce (I3). Losing the read costs a miss; keeping it wrong
	// costs a wrong build, and the asymmetry decides it.
	//
	// 125 of one profile's entries, all `/app/package.json` and npm's own
	// files: genuine reads, resolved through a handle that was not this step's
	// (E498). What would recover them is knowing the two roots hold the same
	// bytes, which is the question the digest was going to answer anyway.
	if mounts != "" && under(clean, []string{mounts}) {
		return "", false
	}

	return p, true
}
