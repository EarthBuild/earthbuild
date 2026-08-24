// Package image turns an OCI image into layers this engine can materialise.
//
// The boundary between hash worlds lives here. A registry addresses content by
// SHA-256 and that is how blobs are fetched and verified; a layer's identity in
// this engine is ℋ over its unpacked tree (green paper §3.3a). The two are
// disjoint namespaces, and this package is the only place both appear.
//
// Everything it reads is untrusted. A registry serves bytes on behalf of
// whoever pushed them, so an entry naming a path outside the layer is an
// attempt to write to the host, not a malformed archive to work around.
package image

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/fstime"
)

// Unpack writes a layer tar into dir.
//
// Refuses, rather than sanitises, any entry that would write outside dir.
// Silently rewriting a hostile path to a safe one would unpack an image that
// does not match its digest, which is a different lie from the one being told.
func Unpack(r io.Reader, dir string) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve the unpack root: %w", err)
	}

	// Resolve the root's own symlinks before comparing anything against it.
	// Without this, a root under /var on macOS - where /var is a symlink to
	// /private/var - is compared against resolved paths beginning /private/var,
	// the prefix check fails, and every legitimate entry is refused as an escape.
	// Comparing a resolved path with an unresolved one is the bug; resolving both
	// ends is the fix.
	resolved, err := filepath.EvalSymlinks(root)
	if err == nil {
		root = resolved
	}

	tr := tar.NewReader(r)

	// What *this* layer has written. A later layer replacing an earlier one's
	// file is the whole of what layering means; one layer naming a path twice
	// is an archive that cannot be trusted to mean anything, and choosing the
	// last of them would be a guess about which entry was intended.
	//
	// The guard used to be O_EXCL against the filesystem, which cannot tell the
	// two apart - so every image with more than one layer failed to unpack, and
	// almost every real base image has more than one. alpine has exactly one,
	// which is why nothing noticed.
	written := map[string]bool{}

	// folded maps a lower-cased path to the one this layer actually wrote.
	//
	// A developer's Mac keeps the layer store on a case-insensitive filesystem
	// by default, so an image containing `Foo` and `foo` loses one - and the
	// survivor holds the other's contents under its own name, which is a wrong
	// image produced in silence. Node and TypeScript packages collide this way
	// often enough that it is not a curiosity.
	folded := map[string]foldedEntry{}

	// Directory modes are applied at the end, deepest first. A layer may ship a
	// directory nothing may write to - `maven:3.8.5-openjdk-17` does, with
	// `usr/bin` - and the files inside it come *after* it in the archive, so
	// applying the mode on creation made every one of them fail with
	// "permission denied". A directory's mode describes the image, not the
	// unpacking of it.
	var dirs []*tar.Header

	// relaxed are directories an *earlier* layer left read-only, made writable
	// so this one can add to them and put back when it is done. The mode is
	// real by then - it was applied at the end of that layer - so this is not
	// the same case as a mode declared and deferred within one layer, and needs
	// its own answer.
	relaxed := map[string]os.FileMode{}

	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			applyErr := applyDirModes(dirs)
			if applyErr != nil {
				return applyErr
			}

			return restoreModes(relaxed, dirs)
		}

		if err != nil {
			return fmt.Errorf("read the layer archive: %w", err)
		}

		target, err := safePath(root, h.Name)
		if err != nil {
			return err
		}

		// **Redundant, and here to be read by a machine.** `safePath` is the
		// guard: it refuses an empty or absolute name, `..` at any depth, and an
		// entry whose parent resolves through a symlink out of the layer -
		// which is the vector a `..` check alone misses, because there the name
		// is innocent and only the path is not.
		//
		// CodeQL cannot see it, because the check is a function away from the
		// use rather than inline at it, and reports this loop as Zip Slip. Its
		// documented remedy is `!strings.Contains(name, "..")`, which is weaker
		// than what is already here: it rejects legitimate entries like
		// `foo..bar`, says nothing about absolute names, and does not address
		// symlinks at all. Adopting it would trade a real guard for a
		// recognisable one.
		//
		// So this asserts containment where the analyser is looking, in the form
		// it understands, and states the same property `safePath` returned. If
		// the two ever disagree the unpack stops, which is the right outcome for
		// a disagreement about whether a write leaves the layer.
		err = insideRoot(root, target, h.Name)
		if err != nil {
			return err
		}

		// Whiteouts are markers, not files: they name a deletion, and writing
		// them literally would put `.wh.foo` into the merged filesystem instead
		// of removing `foo` from it.
		handled, err := whiteout(h, target)
		if err != nil {
			return err
		} else if handled {
			continue
		}

		if h.Typeflag == tar.TypeDir {
			// Kept whole rather than as a path and a mode: setMeta wants the
			// times too, and a second representation of the same header is a
			// second thing to keep in step.
			copied := *h
			copied.Name = target
			dirs = append(dirs, &copied)
		}

		err = relax(filepath.Dir(target), relaxed)
		if err != nil {
			return err
		}

		err = writeEntry(tr, h, root, target, written, folded)
		if err != nil {
			return err
		}
	}
}

// safePath resolves an entry name inside root, refusing anything that escapes.
//
// Two escapes are checked, and the second is the one usually missed: a literal
// `../` in the name, and a path whose *parent* is a symlink pointing out of the
// layer. An archive can create `link -> /tmp` and then write `link/x`, which
// contains no `..` at all and lands outside the layer regardless.
// insideRoot asserts that a resolved entry path lies within the layer.
//
// **The guard is `safePath`; this is the same statement placed where the writes
// are.** CodeQL traces `h.Name` through `filepath.Join` into `target` and out to
// `os.MkdirAll` in `writeEntry`, and reports Zip Slip because the check it can
// see is a function call away. Its documented remedy - `!strings.Contains(name,
// "..")` - is weaker than what is already here: it refuses legitimate entries
// like `foo..bar`, ignores absolute names, and cannot see the vector where the
// name is innocent and the parent is a symlink out of the layer (E628).
//
// So the property is asserted twice: once where the path is derived and once
// immediately before the syscalls that use it. One function, two call sites, so
// there is one rule rather than two that must agree - and a disagreement between
// them stops the unpack, which is the right outcome for a disagreement about
// whether a write leaves the layer.
func insideRoot(root, target, name string) error {
	if target == root || strings.HasPrefix(target, root+string(filepath.Separator)) {
		return nil
	}

	return fmt.Errorf("layer entry %q resolved to %s, which is outside the layer", name, target)
}

func safePath(root, name string) (string, error) {
	if name == "" {
		return "", errors.New("layer entry has an empty name")
	}

	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("layer entry %q names an absolute path", name)
	}

	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("layer entry %q escapes the layer", name)
	}

	// **The root is resolved too, or the comparison below is not like with
	// like.** `EvalSymlinks` on a parent resolves the *root's* own symlinks as
	// well - on darwin `/tmp` is `/private/tmp` - so a resolved parent compared
	// against an unresolved root differs for every top-level entry, and the
	// guard refused `bin`, `etc` and everything else at depth one whenever the
	// store sat under a symlinked path. Found by a test asking whether a name
	// containing `..` is still unpacked: `foo..bar` was refused, and the reason
	// had nothing to do with the dots (E628).
	//
	// A root that cannot be resolved is used as given: it may not exist yet,
	// which is not this function's business.
	realRoot := root

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err == nil {
		realRoot = resolvedRoot
	}

	target := filepath.Join(root, clean)

	// An entry naming the layer's own root - `./`, which every tar built with
	// `tar -C rootfs .` begins with, busybox's included. It is the one entry
	// that cannot escape, and checking its *parent* looked outside the layer and
	// refused it.
	if target == root {
		return target, nil
	}

	// The parent must resolve to somewhere inside root once symlinks are
	// followed. EvalSymlinks on the parent rather than the target, because the
	// target itself does not exist yet.
	parent := filepath.Dir(target)

	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolve the parent of layer entry %q: %w", name, err)
		}

		// The parent has not been created yet, which is normal: entries arrive in
		// tree order. Nothing to follow, so nothing to escape through.
		return target, nil
	}

	if resolved != realRoot && !strings.HasPrefix(resolved, realRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("layer entry %q writes through a symlink out of the layer, to %s",
			name, resolved)
	}

	return target, nil
}

func writeEntry(
	tr *tar.Reader, h *tar.Header, root, target string,
	written map[string]bool, folded map[string]foldedEntry,
) error {
	// Immediately before the syscalls, so the assertion dominates the sink
	// rather than sitting a call away from it. See insideRoot.
	err := insideRoot(root, target, h.Name)
	if err != nil {
		return err
	}

	//nolint:gosec // a mode a build decided; §3.3 counts it as part of the layer
	err = os.MkdirAll(filepath.Dir(target), 0o755)
	if err != nil {
		return fmt.Errorf("create the parent of %q: %w", h.Name, err)
	}

	// A directory is the one kind that may legitimately already be there: two
	// layers both containing `/usr/bin` are not in conflict, and MkdirAll says
	// so. Everything else replaces what a lower layer left.
	if h.Typeflag != tar.TypeDir {
		err := replacing(h, target, written, folded)
		if err != nil {
			return err
		}
	} else if written[target] {
		return fmt.Errorf("%q: the layer names it twice", h.Name)
	} else {
		written[target] = true
		folded[strings.ToLower(target)] = foldedEntry{target: target, name: h.Name}
	}

	switch h.Typeflag {
	case tar.TypeDir:
		// Permissive now, the archive's mode later: see applyDirModes.
		//nolint:gosec // a mode a build decided; §3.3 counts it as part of the layer
		err := os.MkdirAll(target, 0o755)
		if err != nil {
			return fmt.Errorf("create directory %q: %w", h.Name, err)
		}

		// setMeta would chmod it here, which is the thing being deferred.
		return nil

	case tar.TypeReg:
		err := writeFile(tr, h, target)
		if err != nil {
			return err
		}

	case tar.TypeSymlink:
		// The target is not validated: a symlink *pointing* outside the layer is
		// legitimate - /bin/sh -> /busybox is resolved inside the step's own root
		// at run time. What must never happen is this unpacker following it, and
		// safePath is what prevents that.
		err := os.Symlink(h.Linkname, target)
		if err != nil {
			return fmt.Errorf("create symlink %q: %w", h.Name, err)
		}

	case tar.TypeLink:
		source, err := safePath(root, h.Linkname)
		if err != nil {
			return fmt.Errorf("hardlink %q: %w", h.Name, err)
		}

		err = os.Link(source, target)
		if err != nil {
			return fmt.Errorf("create hardlink %q: %w", h.Name, err)
		}

	default:
		// Character devices, fifos and sockets. The comment here used to say
		// they "need privilege this may not have, and a base image rarely
		// carries one" - which is two claims in one sentence, and a fifo needs
		// no privilege at all. The same sentence in `copyTree` cost this engine
		// every deletion it ever made (E88).
		//
		// Created where the OS allows and left out where it does not, which is
		// strictly more than skipping and never less.
		placed, err := makeSpecial(h, target)
		if err != nil {
			return fmt.Errorf("create %q: %w", h.Name, err)
		}

		// Nothing there to carry metadata. Skipping the entry and then setting
		// its mode anyway is what reported `no such file or directory` for
		// `dev/console` in every Debian base image (E108).
		if !placed {
			return nil
		}
	}

	return setMeta(h, target)
}

// replacing clears whatever a lower layer left at a path, and refuses a layer
// that names one twice.
//
// The two are different and the code could not tell them apart: it used O_EXCL
// against the filesystem, which fails identically for "this archive is
// malformed" and "a later layer is replacing an earlier one's file" - and the
// second is the whole of what layering means. Every image with more than one
// layer failed to unpack. alpine has exactly one, which is why nothing noticed.
func replacing(h *tar.Header, target string, written map[string]bool, folded map[string]foldedEntry) error {
	if written[target] {
		return fmt.Errorf("%q: the layer names it twice", h.Name)
	}

	// Only where the filesystem cannot tell them apart: on a case-sensitive one
	// both paths exist and the image is unpacked exactly as it was built.
	key := strings.ToLower(target)
	if other, clash := folded[key]; clash && other.target != target && sameFile(other.target, target) {
		// The *root* is named, not the deepest directory. There is more than
		// one candidate and they move independently - the layer store and the
		// image cache are the same directory by default and
		// EARTH_IMAGE_CACHE_DIR separates them - so "the build cache" sent a
		// reader who had already moved their build cache looking at the wrong
		// one. Naming the deepest directory instead was no better: it pointed
		// at `.pulling-3768822342/usr/lib/xtables`, a staging path that lives
		// for the length of one pull. The root is the thing the reader can
		// actually move, which is what the next line tells them to do.
		return fmt.Errorf(
			"%q and %q differ only in case, and this filesystem cannot hold both"+
				"\n  the image cannot be unpacked without losing one of them"+
				"\n  a case-sensitive volume is the way round it",
			other.name, h.Name)
	}

	folded[key] = foldedEntry{target: target, name: h.Name}
	written[target] = true

	fi, err := os.Lstat(target)
	if err != nil {
		// Nothing there is nothing to replace, which is the ordinary case for
		// the first layer that writes a path.
		return nil //nolint:nilerr // absence is not a failure here
	}

	// RemoveAll rather than Remove, because what is being replaced may be a
	// directory with contents - an image replacing a directory with a file is
	// ordinary enough that docker's own do it.
	if fi.IsDir() {
		removeErr := os.RemoveAll(target)
		if removeErr != nil {
			return fmt.Errorf("replace the directory %q: %w", h.Name, removeErr)
		}

		return nil
	}

	err = os.Remove(target)
	if err != nil {
		return fmt.Errorf("replace %q: %w", h.Name, err)
	}

	return nil
}

func writeFile(tr *tar.Reader, h *tar.Header, target string) error {
	//nolint:gosec // the archive's mode
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode))
	if err != nil {
		return fmt.Errorf("create %q: %w", h.Name, err)
	}

	defer f.Close()

	// Copy bounded by the declared size, so a header claiming one byte cannot
	// stream a gigabyte into the layer store.
	_, err = io.CopyN(f, tr, h.Size)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("write %q: %w", h.Name, err)
	}

	return nil
}

// setMeta restores mode and mtime.
//
// The mtime carries nanoseconds where the archive does: ustar headers hold whole
// seconds and only a PAX extension carries more, so an unpacker that took the
// header's seconds alone would clamp every base image to second precision. That
// is precisely the defect that makes cargo's incremental cache rebuild the world
// (I8).
func setMeta(h *tar.Header, target string) error {
	if h.Typeflag == tar.TypeSymlink {
		// **Mode belongs to the target and the time does not.** A symlink has
		// an mtime of its own, and a layer's identity covers it (§3.3), so
		// skipping the whole of setMeta for links left every one of them
		// stamped with the moment of the unpack.
		//
		// The reasoning that produced the skip was right about `os.Chmod` and
		// `os.Chtimes` - both follow a link, and following one here is what
		// this unpacker must never do - and wrong to conclude there was
		// nothing to set. `Lchtimes` sets a link's own time without following
		// it, which `layer/unpack.go` has always done for the same reason.
		//
		// Alpine carries 335 of them and every one differed between two
		// unpacks of the same bytes, so the placed layer had a different
		// identity on every machine: no L2 hit ever crossed one, and in the
		// fleet it would have read as a corrupted transfer (E546).
		if h.ModTime.IsZero() {
			return nil
		}

		err := fstime.Lchtimes(target, h.ModTime, h.ModTime)
		if err != nil {
			return fmt.Errorf("set mtime on the link %q: %w", h.Name, err)
		}

		return nil
	}

	err := os.Chmod(target, os.FileMode(h.Mode)) //nolint:gosec // the archive's mode
	if err != nil {
		return fmt.Errorf("set mode on %q: %w", h.Name, err)
	}

	// Ownership and extended attributes, which this unpacker did not carry at
	// all: every base image's files were owned by whoever ran the build, and a
	// `setcap` grant in the archive reached the layer as an ordinary binary.
	// Green paper §3.3 lists both among what a layer records (E92).
	err = applyOwner(h, target)
	if err != nil {
		return fmt.Errorf("set ownership on %q: %w", h.Name, err)
	}

	err = applyXattrs(h, target)
	if err != nil {
		return fmt.Errorf("set extended attributes on %q: %w", h.Name, err)
	}

	if h.ModTime.IsZero() {
		return nil
	}

	err = os.Chtimes(target, h.ModTime, h.ModTime)
	if err != nil {
		return fmt.Errorf("set mtime on %q: %w", h.Name, err)
	}

	return nil
}

// applyDirModes gives directories the modes their archive declared, once
// everything is written.
//
// Deepest first, so a directory that denies writing is never made read-only
// before the directory beneath it has been given its own mode.
func applyDirModes(dirs []*tar.Header) error {
	sort.Slice(dirs, func(i, j int) bool {
		return strings.Count(dirs[i].Name, string(os.PathSeparator)) >
			strings.Count(dirs[j].Name, string(os.PathSeparator))
	})

	for _, h := range dirs {
		err := os.Chmod(h.Name, os.FileMode(h.Mode)) //nolint:gosec // the archive's mode
		if err != nil {
			return fmt.Errorf("set mode on %q: %w", h.Name, err)
		}

		if h.ModTime.IsZero() {
			continue
		}

		// I8: an image's timestamps are part of what it is, and a build that
		// stamped them with the moment of unpacking would produce a different
		// layer every time.
		err = os.Chtimes(h.Name, h.ModTime, h.ModTime)
		if err != nil {
			return fmt.Errorf("set times on %q: %w", h.Name, err)
		}
	}

	return nil
}

// RemoveAll deletes a tree that may contain directories nothing may write to.
//
// `os.RemoveAll` cannot: a layer may ship a directory with a mode that denies
// writing - `maven:3.8.5-openjdk-17` ships `usr/bin` that way - and removing a
// file needs write permission on the directory holding it, not on the file. So
// a half-pulled image could not be cleared away, and the staging directory it
// was in stayed for ever.
//
// Modes are restored to something writable on the way down rather than
// preserved: the tree is being deleted, so nothing depends on them again.
func RemoveAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil {
		return nil
	}

	// Second attempt, having made every directory writable. Walk errors are
	// dropped: a path that cannot be walked is one RemoveAll will report on
	// properly below, and reporting it twice helps nobody.
	_ = filepath.Walk(path, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.IsDir() {
			return nil //nolint:nilerr // see above
		}

		// 0700, not 0600: this is a directory, and a directory with no execute
		// bit cannot be entered, so the walk that is about to remove it would
		// fail. The minimum that works is the minimum available.
		_ = os.Chmod(p, 0o700) //nolint:gosec // see above

		return nil
	})

	return os.RemoveAll(path)
}

// relax makes a directory writable if it is not, remembering what it was.
//
// Only what an earlier layer left behind: a directory this layer created is
// already writable, because its declared mode is applied at the end.
func relax(dir string, relaxed map[string]os.FileMode) error {
	if _, seen := relaxed[dir]; seen {
		return nil
	}

	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm()&0o300 == 0o300 {
		return nil //nolint:nilerr // a missing parent is created by writeEntry
	}

	err = os.Chmod(dir, fi.Mode().Perm()|0o300)
	if err != nil {
		return fmt.Errorf("make %q writable to add to it: %w", dir, err)
	}

	relaxed[dir] = fi.Mode().Perm()

	return nil
}

// restoreModes puts back what relax changed, leaving alone anything this layer
// declared a mode for - that one is the image's own and has just been applied.
func restoreModes(relaxed map[string]os.FileMode, dirs []*tar.Header) error {
	declared := make(map[string]bool, len(dirs))
	for _, h := range dirs {
		declared[h.Name] = true
	}

	paths := make([]string, 0, len(relaxed))
	for p := range relaxed {
		paths = append(paths, p)
	}

	sort.Strings(paths)

	for _, p := range paths {
		if declared[p] {
			continue
		}

		err := os.Chmod(p, relaxed[p])
		if err != nil {
			return fmt.Errorf("restore the mode on %q: %w", p, err)
		}
	}

	return nil
}

// sameFile reports whether two names reach the same file, which on a
// case-insensitive filesystem is how `Foo` and `foo` behave.
//
// Asked of the filesystem rather than assumed from the platform: a Mac may have
// a case-sensitive volume, and a Linux machine may have a case-insensitive
// mount. The question is about this directory, not about this operating system.
func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}

	fb, err := os.Stat(b)
	if err != nil {
		return false
	}

	return os.SameFile(fa, fb)
}

// foldedEntry is a path this layer wrote, kept under its case-folded key so a
// collision can name both entries as the archive wrote them.
type foldedEntry struct{ target, name string }
