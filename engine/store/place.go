package store

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fstime"
)

// Placing a tree into the store: hard links where it can, copies where it must,
// and every entry renamed over its destination rather than written in place.
//
// Lives beside the store rather than beside the image cache, because this is
// how a *layer* is written and the image cache is only one thing that writes
// one. The guest needs the same mechanism over the same store (E541), and it
// cannot import the executor.

// PlaceConfig copies an image's declared configuration beside its tree.
//
// The configuration follows the tree, so every node that links this entry has
// it and not only the one that pulled it.
//
// After the tree, because the tree is what creates the destination's parent -
// writing first failed with ENOENT into a dropped error, and the symptom was an
// image that declared an entrypoint being reported as declaring none.
func PlaceConfig(shared, dest string) error {
	b, err := os.ReadFile(shared + ConfigSuffix) //nolint:gosec // a path this engine derived
	if err == nil {
		//nolint:gosec // the same engine-derived path as the read above
		_ = os.WriteFile(dest+ConfigSuffix, b, 0o600)
	}

	return nil
}

// Populated reports whether a directory exists and holds anything.
func Populated(dir string) bool {
	entries, err := os.ReadDir(dir)

	return err == nil && len(entries) > 0
}

// LinkTree reproduces a tree with hard links, falling back to a copy across
// filesystems.
// LinkTree reproduces a tree, deferring restrictive directory modes.
//
// The same rule the unpacker follows, for the same reason and found the same
// way: an image may ship a directory nothing may write to, and creating it with
// that mode means nothing can be put inside it. `maven:3.8.5-openjdk-17` ships
// `usr/bin` at 0555, and it fails here as surely as it failed there.
func LinkTree(src, dst string) error { return linkTreeInto(src, dst, false) }

func linkTreeInto(src, dst string, exclusive bool) error {
	// name -> what the directory should end with, applied once everything is in
	// place. The mode because a restrictive one cannot be written into; the
	// mtime because *creating* the entries beneath a directory changes it, so
	// the time it should carry is only restorable after they are all there.
	dirs := map[string]dirMeta{}

	// Every non-directory entry, placed after the walk has made the directories.
	var files []linkJob

	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)

		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}

			dirs[target] = dirMeta{mode: info.Mode().Perm(), mtime: info.ModTime()}

			// A symlink already sitting where a directory belongs is removed
			// rather than followed. The store is shared with the guest, which
			// runs somebody's RUN command, so a step can leave a link there
			// pointing anywhere - and MkdirAll follows it, which turns "the
			// guest may write anywhere in the store" into "the guest may write
			// anywhere this process can".
			//
			// Sound because WalkDir is top-down: every directory on a path is
			// visited before anything inside it, so each component is a real
			// directory by the time its children are written.
			fi, err := os.Lstat(target)
			if err == nil && fi.Mode()&os.ModeSymlink != 0 {
				err = os.Remove(target)
				if err != nil {
					return fmt.Errorf("clear a symlink at %s: %w", target, err)
				}
			}

			// Permissive enough to write into and no more; the mode the
			// image declared is applied once everything is in place.
			return os.MkdirAll(target, 0o750)
		}

		// A symlink is recreated rather than linked: hard-linking one would tie
		// the two trees together through a name that may itself be replaced.
		// **Placed under a temporary name and renamed over the target.**
		// `Remove` then create is a TOCTOU between two builds placing the same
		// image: both remove, one creates, the other gets `file exists`.
		// Rename replaces in one step, so the loser overwrites with identical
		// bytes and nobody fails (E142).
		//
		// Per *entry*, not per tree. A layer directory in the store may already
		// be mounted as an overlay lowerdir by another build: it may be filled
		// in and never swapped, and a staged tree renamed into place takes the
		// directory out from under that mount - which is `fork/exec /bin/sh:
		// input/output error` in a step that has nothing to do with images.
		// That version was written, measured, and reverted (E141).
		// Collected rather than placed here. The walk must create every parent
		// before its children, which is inherently ordered; placing the entries
		// is not, and it is where the time goes - 17,580 of them for
		// golang:1.26.5-alpine3.24, each a syscall's latency and no work.
		job := linkJob{
			from: p, to: target,
			symlink:   d.Type()&os.ModeSymlink != 0,
			exclusive: exclusive,
		}

		// A recreated symlink is a *new* link, and a new link's time is now.
		// Hard-linked files keep theirs because they are the same inode; a
		// symlink has none to keep, so the time it should carry is read here
		// and restored where it is made (E545).
		if job.symlink {
			info, err := d.Info()
			if err != nil {
				return err
			}

			job.mtime = info.ModTime()
		}

		files = append(files, job)

		return nil
	})
	if err != nil {
		return fmt.Errorf("place the image at %s: %w", dst, err)
	}

	err = placeAll(files)
	if err != nil {
		return fmt.Errorf("place the image at %s: %w", dst, err)
	}

	// Deepest first, so a directory that denies writing is never made read-only
	// before the one beneath it has been given its own mode.
	paths := make([]string, 0, len(dirs))
	for p := range dirs {
		paths = append(paths, p)
	}

	sort.Slice(paths, func(i, j int) bool {
		return strings.Count(paths[i], string(os.PathSeparator)) >
			strings.Count(paths[j], string(os.PathSeparator))
	})

	for _, p := range paths {
		err := os.Chmod(p, dirs[p].mode)
		if err != nil {
			return fmt.Errorf("set the mode on %s: %w", p, err)
		}

		// After the mode, and after everything beneath it: this is the last
		// thing done to a directory, because anything done to one afterwards
		// would put the clock back into the layer's identity.
		err = os.Chtimes(p, dirs[p].mtime, dirs[p].mtime)
		if err != nil {
			return fmt.Errorf("set the time on %s: %w", p, err)
		}
	}

	return nil
}

// dirMeta is what a directory should carry once its contents are in place.
type dirMeta struct {
	mtime time.Time
	mode  os.FileMode
}

// CopyFile is the fallback when a link cannot be made.
func CopyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // a path inside the image cache
	if err != nil {
		return err
	}

	defer func() { _ = in.Close() }()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	//nolint:gosec // inside the layer store
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}

	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}

	return out.Close()
}

// PlaceAtomically creates an entry beside its destination and renames it over.
//
// The destination may exist, may be being read by another build through a
// mount, and may be being written by one - a layer directory in the store is
// shared. `Rename` replaces without a window in which the target is absent;
// `Remove` then create *is* that window.
//
// The temporary name is in the destination's own directory, because a rename
// across filesystems is neither atomic nor permitted.
func PlaceAtomically(target string, write func(tmp string) error) error {
	dir := filepath.Dir(target)

	f, err := os.CreateTemp(dir, ".place-")
	if err != nil {
		return fmt.Errorf("stage %s: %w", target, err)
	}

	tmp := f.Name()
	_ = f.Close()

	// Removed first: CreateTemp made a regular file and the writer may need to
	// make a symlink, which cannot be created over one.
	err = os.Remove(tmp)
	if err != nil {
		return fmt.Errorf("stage %s: %w", target, err)
	}

	err = write(tmp)
	if err != nil {
		_ = os.Remove(tmp)

		return err
	}

	err = os.Rename(tmp, target)
	if err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("place %s: %w", target, err)
	}

	return nil
}

// linkJob is one entry of a tree waiting to be placed.
// The two flags last, so they share one word rather than being padded apart by
// the fields between them: one of these exists per entry placed, and the order
// was costing eight bytes each (govet fieldalignment).
type linkJob struct {
	from, to string
	// mtime is the time a recreated symlink should carry. Symlinks only:
	// everything else is hard-linked and keeps its own.
	mtime   time.Time
	symlink bool
	// exclusive says the destination is one nobody else can reach, so the entry
	// can be created directly instead of being staged and renamed over.
	exclusive bool
}

// placeAll places every entry, several at a time.
//
// **The walk is ordered and the placing is not.** A directory must exist before
// anything inside it, which is why the walk creates them as it goes; a hard link
// into a directory that already exists depends on nothing else in the tree. That
// is the whole of the concurrency, and it is worth having because the cost here
// is syscall latency rather than work: 17,580 entries for a Go base image, at
// roughly 370µs each, is 6.5 seconds of a 45-second build spent waiting.
//
// Bounded by CPU count rather than unbounded: the kernel serialises much of this
// anyway, and thousands of goroutines each holding an open file would trade one
// bottleneck for a worse one.
func placeAll(files []linkJob) error {
	if len(files) == 0 {
		return nil
	}

	// **Bounded by CPU count, and measured rather than assumed.** Linking is a
	// syscall that waits on the filesystem rather than on this process, so the
	// obvious reading is that more workers than cores would help. They do not:
	// on APFS, 20,000 links run at about 2,800 a second at 16, 32, 64, 128 and
	// 256 workers alike - the filesystem serialises the metadata update, and
	// the extra goroutines queue behind it.
	//
	// Left at the CPU count because that is the number that is right when the
	// filesystem is *not* the limit. Anyone tempted to raise it should measure
	// their own filesystem first; this one has nothing to give.
	workers := min(runtime.NumCPU(), len(files))

	jobs := make(chan linkJob)

	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		bad error
	)

	for range workers {
		wg.Go(func() {
			for j := range jobs {
				// **Drained, never abandoned.** A worker that returned on the
				// first error stopped receiving, and the caller - still sending
				// on an unbounded number of entries through a channel with no
				// buffer - blocked on a send nobody would ever take. Forever, at
				// no CPU, with the work already done and nothing on screen to
				// say what was being waited for.
				//
				// *A producer that outlives its consumers.* Skipping the work
				// costs the remaining entries a channel receive each and keeps
				// the one property the caller depends on: that this returns.
				mu.Lock()
				stop := bad != nil
				mu.Unlock()

				if stop {
					continue
				}

				err := placeOne(j)
				if err != nil {
					mu.Lock()

					// The first failure is the one reported: later ones are
					// usually consequences, and a caller given the last error
					// out of a race is given a different story every run.
					if bad == nil {
						bad = err
					}

					mu.Unlock()
				}
			}
		})
	}

	for _, j := range files {
		jobs <- j
	}

	close(jobs)
	wg.Wait()

	return bad
}

// placeOne places a single entry, atomically, exactly as the serial version did.
func placeOne(j linkJob) error {
	if j.exclusive {
		return placeDirectly(j)
	}

	if j.symlink {
		// A symlink is recreated rather than linked: hard-linking one would tie
		// the two trees together through a name that may itself be replaced.
		link, err := os.Readlink(j.from)
		if err != nil {
			return err
		}

		return PlaceAtomically(j.to, func(tmp string) error {
			err := os.Symlink(link, tmp)
			if err != nil {
				return err
			}

			// Stamped before the rename, while the name is this call's own.
			return fstime.Lchtimes(tmp, j.mtime, j.mtime)
		})
	}

	return PlaceAtomically(j.to, func(tmp string) error {
		// A hard link where the store allows it, a copy where it does not: a
		// separated image cache is often on another filesystem.
		err := os.Link(j.from, tmp)
		if err == nil {
			return nil
		}

		return CopyFile(j.from, tmp)
	})
}

// placeDirectly creates an entry without staging it first.
//
// **Four fewer syscalls per entry**: the atomic form creates a temporary file,
// unlinks it, links, and renames, where this links. Measured on a Go base image
// - 15,808 entries - the atomic form costs 2.3x the direct one, which is most of
// the wall clock of materialising a base.
//
// Sound only where the destination is unreachable by anyone else, which is what
// the flag asserts and what both callers arrange: each fills a temporary
// directory of its own and renames the finished tree into place. The atomic
// dance defends against a second writer to the same entry (E142); where the
// caller has already excluded one, it is paying for a race that cannot happen.
func placeDirectly(j linkJob) error {
	if j.symlink {
		link, err := os.Readlink(j.from)
		if err != nil {
			return err
		}

		err = os.Symlink(link, j.to)
		if err != nil {
			return err
		}

		// The link is new and so is its time. Restoring the one the source
		// carries is what keeps a placed tree's identity a property of the
		// tree rather than of the day it was placed (E545).
		return fstime.Lchtimes(j.to, j.mtime, j.mtime)
	}

	err := os.Link(j.from, j.to)
	if err == nil {
		return nil
	}

	// A hard link where the store allows it, a copy where it does not: a
	// separated image cache is often on another filesystem.
	return CopyFile(j.from, j.to)
}

// LinkTreeExclusive is LinkTree into a destination nobody else can reach.
func LinkTreeExclusive(src, dst string) error { return linkTreeInto(src, dst, true) }
