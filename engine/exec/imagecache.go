package exec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/store"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// pullInto fetches a reference into a directory.
// pullInto fetches an image into a directory and reports what it declared.
type pullInto func(ctx context.Context, ref, dir string) (ocispec.ImageConfig, error)

// ImageCacheKey names an image's place in the shared cache.
//
// **By digest where the reference carries one**, because a digest *is* the
// content: `golang:1.26@sha256:x`, `golang@sha256:x` and a mirror serving the
// same manifest are one set of bytes under three names, and keying on the text
// files them three times. `--pin` rewrites a reference this machine has already
// pulled, so keyed on the text it missed the entry it had just filled and the
// next build fetched the whole image again - 33s for bytes already on the disk.
// A user who takes the advice to pin must not pay for it (E536).
//
// Platform stays in the key even so. This engine resolves to one platform's
// manifest, but an author may write a digest naming a manifest *list* by hand,
// and serving one architecture's bytes for another is a container that will not
// start. The cost of keeping it is nothing.
//
// Hashed rather than sanitised: a reference holds slashes, colons and
// occasionally characters a filesystem will not take, and a digest cannot
// collide by accident the way a substitution can.
func ImageCacheKey(ref, platform string) string {
	// The text is the only identity an unresolved reference has - the point of a
	// tag is that it moves - so it is what names one until something resolves it.
	name := ref

	r, err := image.ParseRef(ref)
	if err == nil && r.Digest != "" {
		name = r.Digest
	}

	sum := sha256.Sum256([]byte(name + "\x00" + platform))

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
	if store.Populated(shared) && !agreesWithKey(shared, platform) {
		_ = image.RemoveAll(shared)
		_ = os.Remove(shared + store.ConfigSuffix)
	}

	if !store.Populated(shared) {
		// Pulled to one side and moved into place, because a half-written entry
		// is worse than none: the next build would find a directory, believe the
		// image was there, and build on a fragment.
		staging, err := os.MkdirTemp(filepath.Dir(shared), ".pulling-*")
		if err != nil {
			mkdirErr := os.MkdirAll(filepath.Join(root, "imagecache"), 0o750)
			if mkdirErr != nil {
				return fmt.Errorf("prepare the image cache: %w", mkdirErr)
			}

			staging, mkdirErr = os.MkdirTemp(filepath.Dir(shared), ".pulling-*")
			if mkdirErr != nil {
				return fmt.Errorf("stage a pull of %s: %w", ref, mkdirErr)
			}
		}

		endPull := phase("image:pull", ref)
		cfg, err := pull(ctx, ref, staging)

		endPull()
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
			_ = os.WriteFile(staging+store.ConfigSuffix, b, 0o600)
		}

		// The configuration moves with the entry it describes.
		_ = os.Rename(staging+store.ConfigSuffix, shared+store.ConfigSuffix)

		err = os.Rename(staging, shared)
		if err != nil {
			_ = image.RemoveAll(staging)

			// Another build got there first, which is a race worth losing: its
			// entry is the same bytes under the same key.
			if !store.Populated(shared) {
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
	if store.Populated(dest) {
		return store.PlaceConfig(shared, dest)
	}

	err := os.MkdirAll(filepath.Dir(dest), 0o750)
	if err != nil {
		return fmt.Errorf("prepare the layer store for %s: %w", ref, err)
	}

	staged, err := os.MkdirTemp(filepath.Dir(dest), ".placing-")
	if err != nil {
		return fmt.Errorf("stage %s: %w", ref, err)
	}

	// Removed, because a clone refuses a destination that exists and MkdirTemp
	// has just made one. The name is still ours - it was created exclusively -
	// so this reserves it without occupying it, and the link path recreates it.
	err = os.Remove(staged)
	if err != nil {
		return fmt.Errorf("stage %s: %w", ref, err)
	}

	endPlaceTree := phase("image:copy", ref)
	err = placeTree(shared, staged)

	endPlaceTree()
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
		if !store.Populated(dest) {
			return fmt.Errorf("place %s in the layer store: %w", ref, err)
		}
	}

	return store.PlaceConfig(shared, dest)
}

// imageRootMode is the mode an unpacked image's own root directory gets.
//
// **`os.MkdirTemp` makes it 0700, and an image root is not 0700.** The staging
// directory *becomes* the image root, so every image this engine unpacked had a
// root no unprivileged process could walk into. Nothing noticed while every
// step ran as root; `USER testuser` then failed with `exec /bin/sh: permission
// denied`, naming a shell that was right there (E735).
//
// 0755 rather than the archive's own entry for `.`: every image anybody ships
// has a 0755 root, the entry is frequently absent, and a root the archive does
// not describe is this engine's to name. What the *contents* may be read by is
// still the image's business, entry by entry.
const imageRootMode = 0o755

// Prefetch puts an image in the shared cache before anything asks for it.
//
// The freely-speculable tier: it moves bytes and changes nothing, so a wrong
// guess costs bandwidth and a right one takes a network round trip off the
// critical path. Nothing is linked anywhere - the image simply becomes local,
// and whichever step turns out to need it finds it already there.
func Prefetch(ctx context.Context, root, ref, platform string, pull pullInto) error {
	shared := filepath.Join(root, "imagecache", ImageCacheKey(ref, platform))
	if store.Populated(shared) {
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

	// The staging directory is about to be the image's root. See imageRootMode.
	err = os.Chmod(staging, imageRootMode)
	if err != nil {
		_ = image.RemoveAll(staging)

		return fmt.Errorf("set the root mode of %s: %w", ref, err)
	}

	// A prefetched entry carries its configuration too, or the build that uses
	// it later finds an image that declares nothing.
	b, err := json.Marshal(cfg)
	if err == nil {
		_ = os.WriteFile(staging+store.ConfigSuffix, b, 0o600)
	}

	_ = os.Rename(staging+store.ConfigSuffix, shared+store.ConfigSuffix)

	err = os.Rename(staging, shared)
	if err != nil {
		_ = image.RemoveAll(staging)
	}

	return nil
}
