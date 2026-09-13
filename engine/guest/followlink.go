package guest

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// linkHops bounds how far a chain is followed.
//
// The kernel's own limit is 40 and a real tree never approaches it: an
// alternatives entry is one hop, a versioned library two. The bound exists for
// the loop, and a chain longer than this is refused rather than followed -
// which costs a hit and cannot cost a wrong build.
const linkHops = 16

// followLink records what a symlink bottoms out to, and reports whether it
// reached the bottom.
//
// **The link alone is not what the step read.** `PathDigestIn` digests the
// entry *at* a path, and for a link that is its mode, ownership and target
// string - never the bytes it leads to. Recording only that keys the step on a
// value which does not move when the file behind the link changes, so two bases
// agreeing about the link and differing about its target satisfy one
// prediction: I3 by omission. The kernel resolves the link inside a single
// `openat`, so the tracer sees one path and there is no second sighting to save
// it.
//
// Both are recorded, and both are needed: the link's own digest catches a
// repoint, the target's catches an edit.
//
// **Contained by os.Root rather than by arithmetic.** A target is data inside
// the step's own filesystem, so following one is a traversal the step chooses.
// `Root` refuses anything resolving outside it - `openat2(RESOLVE_BENEATH)` on
// Linux - which is the check this must not get subtly wrong by hand. An
// absolute target is root-relative because that is what the step itself would
// have resolved inside its mount; one naming a host path therefore lands
// nowhere and is refused, which is the right answer by construction.
//
// False means the caller declares the observation lossy. Every way of not
// reaching the bottom - an escape, a loop, a depth, an unreadable link -
// returns false, so the fallback is the behaviour this replaced.
func (s *Server) followLink(
	w *watcher, root, rel string, uids, gids layer.IDMap,
) bool {
	r, err := os.OpenRoot(root)
	if err != nil {
		return false
	}

	defer func() { _ = r.Close() }()

	// Root-relative and without the leading separator, which is what Root wants.
	at := filepath.Clean("/" + rel)

	for range linkHops {
		fi, err := r.Lstat(at[1:])
		if err != nil {
			return false
		}

		if fi.Mode()&fs.ModeSymlink == 0 {
			// Bottomed out. The entry here was recorded by whichever hop named
			// it, including the first, so there is nothing left to do.
			return true
		}

		target, err := r.Readlink(at[1:])
		if err != nil {
			return false
		}

		if filepath.IsAbs(target) {
			at = filepath.Clean(target)
		} else {
			at = filepath.Clean(filepath.Join(filepath.Dir(at), target))
		}

		// Above the root by way of `..`: Root would refuse the next Lstat
		// anyway, and saying so here keeps the reason with the cause.
		if at == "/" || !filepath.IsAbs(at) {
			return false
		}

		id, err := layer.PathDigestIn(filepath.Join(root, at), uids, gids)
		if err != nil {
			return false
		}

		w.read(at, id)
	}

	return false
}
