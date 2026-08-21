package exec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/image"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// pullInto fetches a reference into a directory.
// pullInto fetches an image into a directory and reports what it declared.
type pullInto func(ctx context.Context, ref, dir string) (ocispec.ImageConfig, error)

// ImageCacheKey names an image's place in the shared cache.
//
// Reference *and* platform, because the same name on two architectures is two
// sets of bytes and serving one for the other is a container that will not
// start. Hashed rather than sanitised: a reference holds slashes, colons and
// occasionally characters a filesystem will not take, and a digest cannot
// collide by accident the way a substitution can.
func ImageCacheKey(ref, platform string) string {
	sum := sha256.Sum256([]byte(ref + "\x00" + platform))

	return hex.EncodeToString(sum[:])
}

// fetchImage puts an image in dest, pulling it only if this machine has not
// seen it before.
//
// The layer store is keyed by node identity, which is right for a step's output
// and wrong for a base image: two targets that both begin `FROM alpine:3.22`
// have different node identities and were pulling the same bytes twice. Keyed
// by reference and platform, the second is a local link.
//
// Linked rather than copied. A layer is read-only to a step (green paper
// §3.3b), so two names for one file is exactly what is wanted: no bytes move
// and no step can write through one name to disturb the other.
func fetchImage(ctx context.Context, root, ref, platform, dest string, pull pullInto) error {
	return fetchImageFrom(ctx, root, ref, platform, dest, pull)
}

// fetchImageFrom is fetchImage with the image cache named separately.
//
// The two are separable because they answer different questions. A layer store
// belongs to a build cache and is thrown away with it; an image is
// content-addressed by reference and platform, identical for every project on
// the machine, and there is no reason for two projects - or two test runs - to
// fetch alpine twice. Keeping them together is what earned this repository a
// rate limit from its own test suite.
func fetchImageFrom(ctx context.Context, imageRoot, ref, platform, dest string, pull pullInto) error {
	root := imageRoot
	shared := filepath.Join(root, "imagecache", ImageCacheKey(ref, platform))

	// An entry whose content contradicts its key is discarded rather than
	// served. The key names a platform, so an entry under it claims to be that
	// platform; one that is not produces `fork/exec /bin/sh: exec format error`
	// in place of a sentence naming both architectures, and only when the cache
	// happens to be warm (E28). A cache that has gone wrong is meant to be
	// thrown away, not to end builds until somebody deletes it by hand - and
	// re-fetching puts the question back where the registry can answer it.
	if populated(shared) && !agreesWithKey(shared, platform) {
		_ = image.RemoveAll(shared)
		_ = os.Remove(shared + configSuffix)
	}

	if !populated(shared) {
		// Pulled to one side and moved into place, because a half-written entry
		// is worse than none: the next build would find a directory, believe the
		// image was there, and build on a fragment.
		staging, err := os.MkdirTemp(filepath.Dir(shared), ".pulling-*")
		if err != nil {
			err := os.MkdirAll(filepath.Join(root, "imagecache"), 0o750)
			if err != nil {
				return fmt.Errorf("prepare the image cache: %w", err)
			}

			staging, err = os.MkdirTemp(filepath.Dir(shared), ".pulling-*")
			if err != nil {
				return fmt.Errorf("stage a pull of %s: %w", ref, err)
			}
		}

		cfg, err := pull(ctx, ref, staging)
		if err != nil {
			_ = image.RemoveAll(staging)

			// The staging directory is gone by the time anyone reads this, so
			// an error naming it names nothing. The image cache is where the
			// unpack was really happening and is the directory a reader can
			// move to a case-sensitive volume - which is exactly what the
			// case-collision refusal goes on to tell them to do.
			return fmt.Errorf("%w\n  while filling the image cache at %s",
				err, filepath.Join(root, "imagecache"))
		}

		// Beside the entry rather than inside it, so it is never linked into a
		// step's filesystem: what an image *declares* is not part of what it
		// ships. Written before the rename, so an entry that becomes visible
		// has its configuration visible with it.
		//
		// It belongs to the shared entry and not to one node's layer directory,
		// which is where it went first: a second target naming the same image
		// links the tree from here and never pulls, so a per-node file existed
		// only for whichever node happened to pull it - and `RUN --entrypoint`
		// then reported that the image declared no entrypoint.
		b, err := json.Marshal(cfg)
		if err == nil {
			_ = os.WriteFile(staging+configSuffix, b, 0o600)
		}

		// The configuration moves with the entry it describes.
		_ = os.Rename(staging+configSuffix, shared+configSuffix)

		err = os.Rename(staging, shared)
		if err != nil {
			_ = image.RemoveAll(staging)

			// Another build got there first, which is a race worth losing: its
			// entry is the same bytes under the same key.
			if !populated(shared) {
				return fmt.Errorf("store %s in the image cache: %w", ref, err)
			}
		}
	}

	// **Existing implies finished.** A layer directory is placed by renaming a
	// staged tree in, so a directory that is there is a directory that is
	// complete - and a build that finds one may mount it without wondering
	// whether somebody is still filling it.
	//
	// Skipping when it exists is the other half, and the important one: writing
	// into a layer another build has *mounted* invalidates that mount, and the
	// step reading through it fails with `input/output error` (E141). An entry
	// is inserted once and never rewritten, which is what every other writer in
	// this store already does (I9).
	if populated(dest) {
		return placeConfig(shared, dest)
	}

	err := os.MkdirAll(filepath.Dir(dest), 0o750)
	if err != nil {
		return fmt.Errorf("prepare the layer store for %s: %w", ref, err)
	}

	staged, err := os.MkdirTemp(filepath.Dir(dest), ".placing-")
	if err != nil {
		return fmt.Errorf("stage %s: %w", ref, err)
	}

	err = linkTree(shared, staged)
	if err != nil {
		_ = image.RemoveAll(staged)

		return err
	}

	err = os.Rename(staged, dest)
	if err != nil {
		_ = image.RemoveAll(staged)

		// Another build placed it first. A rename onto a non-empty directory
		// fails, so the loser is told rather than silently replacing a tree the
		// winner may already have mounted.
		if !populated(dest) {
			return fmt.Errorf("place %s in the layer store: %w", ref, err)
		}
	}

	return placeConfig(shared, dest)
}

// placeConfig copies an image's declared configuration beside its tree.
//
// The configuration follows the tree, so every node that links this entry has
// it and not only the one that pulled it.
//
// After the tree, because the tree is what creates the destination's parent -
// writing first failed with ENOENT into a dropped error, and the symptom was an
// image that declared an entrypoint being reported as declaring none.
func placeConfig(shared, dest string) error {
	b, err := os.ReadFile(shared + configSuffix) //nolint:gosec // a path this engine derived
	if err == nil {
		//nolint:gosec // the same engine-derived path as the read above
		_ = os.WriteFile(dest+configSuffix, b, 0o600)
	}

	return nil
}

// populated reports whether a directory exists and holds anything.
func populated(dir string) bool {
	entries, err := os.ReadDir(dir)

	return err == nil && len(entries) > 0
}

// linkTree reproduces a tree with hard links, falling back to a copy across
// filesystems.
// linkTree reproduces a tree, deferring restrictive directory modes.
//
// The same rule the unpacker follows, for the same reason and found the same
// way: an image may ship a directory nothing may write to, and creating it with
// that mode means nothing can be put inside it. `maven:3.8.5-openjdk-17` ships
// `usr/bin` at 0555, and it fails here as surely as it failed there.
func linkTree(src, dst string) error {
	// name -> the mode it should end with, applied once everything is in place.
	modes := map[string]os.FileMode{}

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

			modes[target] = info.Mode().Perm()

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
		files = append(files, linkJob{from: p, to: target, symlink: d.Type()&os.ModeSymlink != 0})

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
	paths := make([]string, 0, len(modes))
	for p := range modes {
		paths = append(paths, p)
	}

	sort.Slice(paths, func(i, j int) bool {
		return strings.Count(paths[i], string(os.PathSeparator)) >
			strings.Count(paths[j], string(os.PathSeparator))
	})

	for _, p := range paths {
		err := os.Chmod(p, modes[p])
		if err != nil {
			return fmt.Errorf("set the mode on %s: %w", p, err)
		}
	}

	return nil
}

// copyFile is the fallback when a link cannot be made.
func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // a path inside the image cache
	if err != nil {
		return err
	}

	defer func() { _ = in.Close() }()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm()) //nolint:gosec // inside the layer store
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

// Prefetch puts an image in the shared cache before anything asks for it.
//
// The freely-speculable tier: it moves bytes and changes nothing, so a wrong
// guess costs bandwidth and a right one takes a network round trip off the
// critical path. Nothing is linked anywhere - the image simply becomes local,
// and whichever step turns out to need it finds it already there.
func Prefetch(ctx context.Context, root, ref, platform string, pull pullInto) error {
	shared := filepath.Join(root, "imagecache", ImageCacheKey(ref, platform))
	if populated(shared) {
		return nil
	}

	err := os.MkdirAll(filepath.Join(root, "imagecache"), 0o750)
	if err != nil {
		return fmt.Errorf("prepare the image cache: %w", err)
	}

	staging, err := os.MkdirTemp(filepath.Dir(shared), ".pulling-*")
	if err != nil {
		return fmt.Errorf("stage a prefetch of %s: %w", ref, err)
	}

	cfg, err := pull(ctx, ref, staging)
	if err != nil {
		_ = image.RemoveAll(staging)

		return err
	}

	// A prefetched entry carries its configuration too, or the build that uses
	// it later finds an image that declares nothing.
	b, err := json.Marshal(cfg)
	if err == nil {
		_ = os.WriteFile(staging+configSuffix, b, 0o600)
	}

	_ = os.Rename(staging+configSuffix, shared+configSuffix)

	err = os.Rename(staging, shared)
	if err != nil {
		_ = image.RemoveAll(staging)
	}

	return nil
}

// placeAtomically creates an entry beside its destination and renames it over.
//
// The destination may exist, may be being read by another build through a
// mount, and may be being written by one - a layer directory in the store is
// shared. `Rename` replaces without a window in which the target is absent;
// `Remove` then create *is* that window.
//
// The temporary name is in the destination's own directory, because a rename
// across filesystems is neither atomic nor permitted.
func placeAtomically(target string, write func(tmp string) error) error {
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
type linkJob struct {
	from, to string
	symlink  bool
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

	workers := min(runtime.NumCPU(), len(files))

	jobs := make(chan linkJob)

	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		bad error
	)

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := range jobs {
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

					return
				}
			}
		}()
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
	if j.symlink {
		// A symlink is recreated rather than linked: hard-linking one would tie
		// the two trees together through a name that may itself be replaced.
		link, err := os.Readlink(j.from)
		if err != nil {
			return err
		}

		return placeAtomically(j.to, func(tmp string) error {
			return os.Symlink(link, tmp)
		})
	}

	return placeAtomically(j.to, func(tmp string) error {
		// A hard link where the store allows it, a copy where it does not: a
		// separated image cache is often on another filesystem.
		err := os.Link(j.from, tmp)
		if err == nil {
			return nil
		}

		return copyFile(j.from, tmp)
	})
}
