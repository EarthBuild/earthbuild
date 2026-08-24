package guest

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fstime"
)

// copyPath copies a file or a directory, and its callers do not say which.
//
// Every "copy this" in the guest comes through here, because the two that did
// not each grew a second, shorter piece of copying code beside copyTree, and
// each drifted from it in a different way. `SAVE ARTIFACT` of a file lost its
// mtime outright (I8). `COPY` of a file kept the mtime and ignored
// SOURCE_DATE_EPOCH, so a build obeyed the clamp for `COPY --dir tree /x` and
// disobeyed it for `COPY file /x` - reproducibility that turned on how an input
// happened to be spelled.
//
// Neither looked wrong beside the other, which is the point: the fork on "is it
// a directory?" is a fork on how to *walk*, not on how to write a file, and
// every time it was written out at a call site the writing rules were copied
// along with it. Here it is written once, both arms stamp, and a caller cannot
// take half of it.
//
// The root is resolved - a source the Earthfile named directly means the thing
// it names, which is what the reference engine does and what it fails loudly
// trying to do when the target is not there. Symlinks found *inside* a tree
// stay symlinks; see copyTree.
//
// Resolution is resolveLast's, not os.Stat's: the difference is the root the
// link's own text is read against, and only one of them is the machine that
// wrote it.
func copyPath(root, src, dst string, opts copyOpts) error {
	if !opts.NoFollow {
		resolved, err := resolveLast(root, src)
		if err != nil {
			return err
		}

		src = resolved
	}

	fi, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}

	// `--symlink-no-follow`, and only reachable with it: without the flag the
	// resolution above has already turned a link into what it names.
	//
	// The result dangles whenever the target was not copied too, and that is
	// what the flag asks for and what the reference produces. `ln -s real link`
	// names a sibling; an image given the link and not `real` has a link to
	// nothing, and an engine that quietly substituted the tree would be
	// deciding the author was mistaken.
	if fi.Mode()&os.ModeSymlink != 0 {
		err = copyLink(src, dst)
		if err != nil {
			return err
		}

		return keepOwn(fi, dst, opts)
	}

	if fi.IsDir() {
		return copyTree(src, dst, opts)
	}

	err = copyFile(src, dst, fi.Mode())
	if err != nil {
		return err
	}

	at := opts.stamp(fi.ModTime())

	err = os.Chtimes(dst, at, at)
	if err != nil {
		return fmt.Errorf("set the mtime on %s: %w", dst, err)
	}

	return keepOwn(fi, dst, opts)
}

// keepOwn gives the destination the source's uid and gid, when asked.
//
// `os.Lchown`, never `os.Chown`: the latter follows a link, so copying a
// symlink would change the ownership of whatever it names - which lives in the
// *source* layer, is shared, and is what the next build reads. A copy that
// mutates its own input is the one thing a content-addressed store cannot
// survive.
//
// A refusal is reported rather than swallowed. Only root may hand a file to an
// arbitrary user, and a build that asked for ownership and silently did not get
// it produces an image whose files belong to the wrong user - a failure that
// surfaces at runtime, in a container, a long way from here.
func keepOwn(fi os.FileInfo, dst string, opts copyOpts) error {
	// `--chown` names the owner outright, so there is nothing to take from the
	// source. Resolved once per copy against the destination image (E419).
	if opts.Chown != "" {
		err := os.Lchown(dst, opts.chownUID, opts.chownGID)
		if err != nil {
			return fmt.Errorf("--chown=%s: set the owner of %s: %w", opts.Chown, dst, err)
		}

		return nil
	}

	if !opts.KeepOwn {
		return nil
	}

	// A caller with nothing to copy the ownership *from* is a bug in this file
	// rather than a condition to tolerate: the first version of the directory
	// pass looked the FileInfo up in a map it never filled, and a nil check
	// that returned quietly would have turned a segfault into a tree whose
	// directories silently kept the running user's group.
	if fi == nil {
		return fmt.Errorf("--keep-own: nothing recorded the ownership of %s", dst)
	}

	uid, gid, ok := ownerOf(fi)
	if !ok {
		return fmt.Errorf("--keep-own: %s does not report ownership on this platform", dst)
	}

	err := os.Lchown(dst, uid, gid)
	if err != nil {
		return fmt.Errorf("--keep-own: set the owner of %s: %w", dst, err)
	}

	return nil
}

// fileID identifies a file within the copy, for spotting hard links.
//
// Device as well as inode: inode numbers are unique per filesystem, not
// globally, and a delta that spans a bind mount would otherwise link two
// unrelated files together - which is worse than the fault being fixed, because
// a later write to one would change the other.
//
// `ok` is false where the platform does not report either, and an unidentified
// file is always copied: linking on a guess is not a trade worth making.
type fileID struct {
	dev, ino uint64
	ok       bool
}

// maxLinkHops bounds a chain of symlinks. Linux uses 40 for the whole
// resolution; this resolves one component, so a chain long enough to reach the
// bound is a loop or an attempt to find one.
const maxLinkHops = 16

// resolveLast follows a symlink at the end of a path, and nothing before it.
//
// Not filepath.EvalSymlinks, which resolves *every* component: the path arrived
// here from within(), which checked the text and did not follow anything, so a
// link planted at any parent would resolve into somewhere that check never saw.
// Only the final component is in question - it is the one the Earthfile named -
// and each hop is put back through within() before the next.
//
// The link's text is read against root rather than against this host. A step
// runs chrooted, so `/opt/app` written by that step means the step's /opt/app;
// resolved here by the host it means the guest's, which is a different
// filesystem that A3 says a step cannot reach. A relative target that climbs
// out with `..` is clamped to root, which is what the kernel does above a chroot
// and therefore what the step that wrote the link saw.
//
// Returns the path unchanged when it is not a link, so callers need no
// condition of their own.
func resolveLast(root, p string) (string, error) {
	for hop := 0; ; hop++ {
		fi, err := os.Lstat(p)
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", p, err)
		}

		if fi.Mode()&os.ModeSymlink == 0 {
			return p, nil
		}

		if hop == maxLinkHops {
			return "", fmt.Errorf("%s is a chain of more than %d symlinks, so it is a loop", p, maxLinkHops)
		}

		target, err := os.Readlink(p)
		if err != nil {
			return "", fmt.Errorf("read symlink %s: %w", p, err)
		}

		next := target
		if !filepath.IsAbs(next) {
			next, err = filepath.Rel(root, filepath.Join(filepath.Dir(p), next))
			if err != nil {
				return "", fmt.Errorf("resolve %s -> %s: %w", p, target, err)
			}
		}

		p, err = within(root, next)
		if err != nil {
			return "", fmt.Errorf("resolve %s -> %s: %w", p, target, err)
		}
	}
}

// copyLink places a symlink with the same text, replacing whatever is there.
//
// os.Symlink refuses an existing path, and a destination is not always empty -
// an earlier step in the same build may have written one. Cleared with
// os.Remove, which does not follow a link, so a symlink planted at the
// destination is deleted rather than followed out of the step's root (A3).
//
// No mode and no mtime: both would apply to the target rather than to the link,
// which is the same reason copyTree leaves them alone for the links inside it.
func copyLink(src, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return fmt.Errorf("read symlink %s: %w", src, err)
	}

	err = os.Remove(dst)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear %s: %w", dst, err)
	}

	err = os.Symlink(target, dst)
	if err != nil {
		return fmt.Errorf("create symlink %s: %w", dst, err)
	}

	return nil
}

// copyTree copies a directory, preserving mode and mtime.
//
// The fallback when a rename cannot work because source and destination are on
// different filesystems - which is the normal case here, since a step's scratch
// is local to the sandbox and the layer store is shared into it.
//
// mtimes are preserved because they are part of a layer's identity (I8): a copy
// that reset them would produce a layer whose digest does not match the one just
// computed.
func copyTree(src, dst string, opts copyOpts) error {
	// Directory modes are applied once everything is in place, deepest first. A
	// tree may contain a directory nothing may write to - `maven`'s image has
	// /root at 0700, and a step that writes /root/.m2 inside it - and creating
	// it with that mode means nothing can be put in it. A directory's mode
	// describes the tree, not the copying of it.
	modes := map[string]os.FileMode{}
	// The source's own entry for each directory, kept for the pass below:
	// ownership and mtime are both applied deepest-first, ownership because
	// handing a directory to another user before its contents are in it can
	// stop this process writing them, and the mtime because writing into a
	// directory is what changes it.
	owners := map[string]os.FileInfo{}
	// seen maps a file's identity to the first path that got it, so a second
	// name for one inode is linked rather than copied.
	seen := map[fileID]string{}

	// `walkErr` rather than `err`, because everything inside this callback that
	// touches the filesystem declares an `err` of its own and every one of them
	// shadowed the parameter (govet shadow). Six sightings in one function, all
	// harmless and all noise - the parameter is checked here and dead
	// afterwards, so naming it for what it is says that once instead of six
	// times.
	walked := filepath.Walk(src, func(p string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, p)
		if err != nil {
			return fmt.Errorf("relative path: %w", err)
		}

		target := filepath.Join(dst, rel)

		switch {
		case fi.IsDir():
			modes[target] = fi.Mode().Perm()
			owners[target] = fi

			// A symlink already where a directory belongs is removed rather
			// than followed, or the copy writes into whatever it names - the
			// shared store, another layer, the guest's own root - and the
			// step's result stops being bounded by the step (A3). The link need
			// not be planted during the copy: the destination is a filesystem
			// an earlier step of this build has already written to.
			//
			// Sound because the walk is top-down: every directory on a path is
			// visited before anything inside it.
			link, lstatErr := os.Lstat(target)
			if lstatErr == nil && link.Mode()&os.ModeSymlink != 0 {
				lstatErr = os.Remove(target)
				if lstatErr != nil {
					return fmt.Errorf("clear a symlink at %s: %w", target, lstatErr)
				}
			}

			//nolint:gosec // a mode a build decided; §3.3 counts it as part of the layer
			mkdirErr := os.MkdirAll(target, 0o755)
			if mkdirErr != nil {
				return fmt.Errorf("create %s: %w", target, mkdirErr)
			}

			// The other half of how an overlay records a removal: a directory
			// that replaces one below it is marked opaque with an xattr, and a
			// copy that dropped the mark would restore the lower directory's
			// contents under a step that deleted them.
			return copyXattrs(p, target)

		case fi.Mode()&os.ModeSymlink != 0:
			link, linkErr := os.Readlink(p)
			if linkErr != nil {
				return fmt.Errorf("read symlink %s: %w", p, linkErr)
			}

			// G122 reads the shape: a path from a walk, used to write. The tree
			// being walked is one this step is assembling into a directory
			// nothing else can see yet, so there is no second writer to race.
			linkErr = os.Symlink(link, target) //nolint:gosec // see above
			if linkErr != nil {
				return fmt.Errorf("create symlink %s: %w", target, linkErr)
			}

			// Mode and time would apply to the link's target, not the link.
			// Ownership and attributes do not: Lchown and Lsetxattr both name
			// the link itself.
			//
			// This branch returned bare `nil` until now, and it was meant to
			// have carried ownership two iterations ago - a scripted edit whose
			// search text did not match wrote nothing and said nothing. Its
			// test skips on a store that cannot carry ownership, which is this
			// one, so the gap had no way to show.
			linkErr = copyXattrs(p, target)
			if linkErr != nil {
				return linkErr
			}

			linkErr = keepOwn(fi, target, opts)
			if linkErr != nil {
				return linkErr
			}

			// A link's own mtime, which `os.Chtimes` cannot set because it
			// follows. The digest records it - `layer.Take` lstats every entry -
			// so a tree with one link in it digested differently after a copy.
			at := opts.stamp(fi.ModTime())

			return fstime.Lchtimes(target, at, at)

		case fi.Mode().IsRegular():
			// A second name for a file already copied is *linked*, not copied
			// again. `layer.Take` records inode identity and says why: "two
			// paths sharing an inode are not two independent copies, and a
			// layer that recorded them as such would lose the link on restore."
			// It recorded it and this copy lost it - the same shape as the
			// mtime invariant this function documents and broke for directories
			// (E87).
			//
			// `alpine`'s /bin is one busybox with several hundred names
			// hard-linked to it, so a delta carrying it became several hundred
			// copies of one executable.
			//
			// Keyed on inode *and device*, because inode numbers are only
			// unique within a filesystem and a delta can span one bind mount.
			if first, ok := seen[idOf(fi)]; ok {
				linkErr := os.Link(first, target)
				if linkErr == nil {
					return nil
				}

				// A filesystem that will not link falls back to copying, which
				// is what this did everywhere until now: the tree is correct
				// and larger, and a build that works is worth more than a link
				// count. Unlike a whiteout, nothing is *lost* by copying.
			} else if k := idOf(fi); k.ok {
				seen[k] = target
			}

			copyErr := copyFile(p, target, fi.Mode())
			if copyErr != nil {
				return copyErr
			}

		default:
			// Devices and fifos, which the previous version skipped with a
			// comment saying they "rarely appear in a delta". **An overlayfs
			// whiteout is a character device**, and it appears in the delta of
			// every step that deletes anything - so every deletion was dropped
			// on the way into the store, and the layer that arrived said
			// nothing had been removed. Measured: `RUN rm /marker.txt` followed
			// by a step that looks for it found it.
			//
			// Reproduced rather than skipped, and where it cannot be, refused:
			// an entry silently missing from a layer is a step's work quietly
			// discarded, which is the failure this branch used to be.
			// `rel` and not `p`: the path inside the image, which is what the
			// author deleted. The internal one is a scratch mount with a
			// generated handle in it and means nothing to a reader.
			placed, copyErr := copySpecial(p, target, "/"+filepath.ToSlash(rel), fi)
			if copyErr != nil {
				return copyErr
			}

			// A deletion recorded as a `.wh.` marker puts nothing at target, so
			// there is nothing there to stamp or own.
			if !placed {
				return nil
			}
		}

		// Before the mtime, because writing an attribute or an owner updates
		// the inode's times and the timestamp has to be the last thing set.
		err = copyXattrs(p, target)
		if err != nil {
			return err
		}

		err = keepOwn(fi, target, opts)
		if err != nil {
			return err
		}

		at := opts.stamp(fi.ModTime())

		err = os.Chtimes(target, at, at)
		if err != nil {
			return fmt.Errorf("set mtime on %s: %w", target, err)
		}

		return nil
	})
	if walked != nil {
		return walked
	}

	// Deepest first, so a directory that denies writing is never made read-only
	// before the one beneath it has been given its own mode.
	paths := make([]string, 0, len(modes))
	for p := range modes {
		paths = append(paths, p)
	}

	sort.Slice(paths, func(i, j int) bool {
		return strings.Count(paths[i], string(os.PathSeparator)) >
			strings.Count(paths[j], string(os.PathSeparator))
	})

	for _, p := range paths {
		err := keepOwn(owners[p], p, opts)
		if err != nil {
			return err
		}

		err = os.Chmod(p, modes[p])
		if err != nil {
			return fmt.Errorf("set the mode on %s: %w", p, err)
		}

		// And the mtime, which the walk above could not set: a directory's
		// mtime changes every time something is written into it, so it can only
		// be restored once its contents are in place - which is what this pass
		// is for.
		//
		// Its absence was the whole of E86's open question. `commit` copies a
		// delta into the store, every directory in the copy took the wall clock
		// as its mtime, and a layer's identity includes mtimes (I8) - so a
		// layer was filed under a digest its own contents no longer produced,
		// and the comment on this function says in as many words that this is
		// the thing not to do.
		//
		// It is also why two builds of one deterministic step produced two
		// layer digests (E81): not the step, this copy. The Content digest was
		// stable throughout, which is exactly the signature of a difference
		// that is only timestamps.
		at := opts.stamp(owners[p].ModTime())

		err = os.Chtimes(p, at, at)
		if err != nil {
			return fmt.Errorf("set the mtime on %s: %w", p, err)
		}
	}

	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // walking our own delta
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}

	defer in.Close()

	// The destination is inside the step's own root, checked by within().
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode) //nolint:gosec // see above
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}

	defer out.Close()

	_, err = io.Copy(out, in)
	if err != nil {
		return fmt.Errorf("copy %s: %w", src, err)
	}

	return nil
}

// mkdirAllStamped makes a path and gives a deterministic time to whatever it had
// to invent.
//
// **The one entry that differed.** Two layers holding the same copied tree were
// compared entry by entry: 193 of them, identical in content, mode and time
// except for the ancestor directory the copy created to hold the tree, which
// carried the wall clock of whichever build made it. Layer identity includes
// mtimes (I8), Κ₁ hashes the identities of a step's base (green paper 4.5), so
// that one directory re-keyed every step above it and a store that once had to
// rebuild never went warm again (E575, E576).
//
// Only what this call creates. A directory that was already there has a time
// that means something - some earlier step wrote it - and stamping it would put
// this copy's mark on somebody else's work.
func mkdirAllStamped(path string, perm os.FileMode, clamp *time.Time) error {
	// Deepest missing ancestor first, so the list is what MkdirAll will make.
	var invented []string

	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		_, err := os.Lstat(p)
		if err == nil {
			break
		}

		invented = append(invented, p)

		if parent := filepath.Dir(p); parent == p {
			break
		}
	}

	err := os.MkdirAll(path, perm)
	if err != nil {
		return err //nolint:wrapcheck // the caller says which copy this was
	}

	// Named `at` because that is what stamp() returns everywhere here, and what
	// TestEveryMtimeIsClampedOrExcused reads to tell a stamped write from a
	// wall-clock one.
	at := fstime.Stamp(clamp, fstime.Invented)

	for _, p := range invented {
		// Best-effort: a directory that cannot be stamped is a layer that
		// digests differently, which costs a rebuild. Failing the copy over it
		// would cost the build.
		_ = fstime.Lchtimes(p, at, at)
	}

	return nil
}
