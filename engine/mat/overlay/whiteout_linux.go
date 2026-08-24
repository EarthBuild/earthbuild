//go:build linux

package overlay

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/EarthBuild/earthbuild/engine/timing"
)

// translator turns a layer's portable deletion markers back into the form
// overlayfs understands, on storage this VM owns.
//
// The layer store is a host directory shared into the sandbox, and a share
// whose host filesystem has no device nodes cannot hold a whiteout (E88). The
// guest therefore writes `.wh.<name>` files, which any filesystem can hold and
// which every registry already uses - and overlayfs cannot read them, so
// somebody has to translate.
//
// Here, because this is the last point before the mount and the first point
// that is inside the VM, where `mknod` works.
//
// **Only layers that contain a marker are copied.** A layer with no deletion in
// it - which is nearly all of them - is used from the store directly, so the
// cost falls exactly on the builds that need it. The copy is remembered by
// layer id, because a stack of thirty layers is materialised for every step of
// a build and translating one twice would be work done to reach the same
// answer.
type translator struct {
	dir string

	mu   sync.Mutex
	done map[string]string
}

func newTranslator(dir string) *translator {
	return &translator{dir: dir, done: map[string]string{}}
}

// use returns the directory to stack for a layer: the store's own, or a
// translated copy where the layer records a deletion.
func (t *translator) use(src, id string) (string, error) {
	// **Before the scan, not after it.** A layer is immutable and named by its
	// content, so whether it carries a marker is a property of the id and can be
	// remembered - and the scan walks the whole layer, which on a golang base is
	// 0.54s. Asking after the scan meant only translations were remembered, and
	// a layer with no markers - nearly all of them - was walked again on every
	// materialise, which is once per step (E529).
	t.mu.Lock()

	if out, ok := t.done[id]; ok {
		t.mu.Unlock()

		return out, nil
	}

	t.mu.Unlock()

	// **The positive answer was already durable and the negative one was not.**
	// A translated layer is a directory the next process finds; "this layer has
	// no markers" lived only in the memo above, which belongs to the guest
	// daemon - and the idle timeout stops that after 30 minutes, so the first
	// build of a session walked the whole base again (E530).
	// The store's note first, because it is the one a fresh VM has: whoever
	// placed the layer knew the answer without looking, and CI gets a new VM
	// for every build, so a note this guest wrote last time never exists there
	// (E531).
	_, err := os.Stat(UnmarkedNote(src))
	if err == nil {
		t.mu.Lock()
		t.done[id] = src
		t.mu.Unlock()

		return src, nil
	}

	_, err = os.Stat(unmarkedFile(t.dir, id))
	if err == nil {
		t.mu.Lock()
		t.done[id] = src
		t.mu.Unlock()

		return src, nil
	}

	endMarkers := timing.Phase("mat:markers", id)
	marked, err := hasMarkers(src)
	endMarkers()

	if err != nil {
		return "", err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Remembered as itself: a layer stacked from the store is the answer to this
	// question just as much as a translated one is.
	if !marked {
		t.done[id] = src

		// Best effort, and deliberately so: a note that cannot be written costs
		// one walk in some later process, which is what happened before it
		// existed. Failing the materialise over it would turn a slow build into
		// a broken one.
		if err := os.MkdirAll(t.dir, 0o750); err == nil {
			_ = os.WriteFile(unmarkedFile(t.dir, id), nil, 0o600)
		}

		return src, nil
	}

	// Another materialise translated it while this one was scanning.
	if out, ok := t.done[id]; ok {
		return out, nil
	}

	out := filepath.Join(t.dir, id)

	// Another build translated it first. Checked before staging, because the
	// cheapest way to win a race is not to enter it.
	if _, err := os.Stat(out); err == nil {
		t.done[id] = out

		return out, nil
	}

	err = os.MkdirAll(t.dir, 0o750)
	if err != nil {
		return "", fmt.Errorf("prepare the translation directory: %w", err)
	}

	// A partial translation must never be stacked, so it is built beside its
	// name and renamed in - the rule the layer store itself follows.
	//
	// **The staging name is asked for, not derived.** It was `<id>.partial`,
	// which is the same path for every builder of that layer: two builds
	// translating one layer, and the second's `RemoveAll` deletes the first's
	// half-written tree. The lock above this is one per materialiser, which is
	// exactly no help across two builds sharing a scratch directory (E142).
	//
	// The same shape as the mount names (E140). A derived name is unique among
	// the things the deriver knows about, and a second process is not one of
	// them.
	tmp, err := os.MkdirTemp(t.dir, "."+id+".partial-")
	if err != nil {
		return "", fmt.Errorf("stage a translation: %w", err)
	}

	err = translate(src, tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)

		return "", err
	}

	err = os.Rename(tmp, out)
	if err != nil {
		_ = os.RemoveAll(tmp)

		// Another build committed the same translation while this one was
		// building it, which is a race worth losing: the id names the layer, so
		// the two results are the same bytes.
		_, statErr := os.Stat(out)
		if statErr != nil {
			return "", fmt.Errorf("commit the translation of %s: %w", id, err)
		}
	}

	t.done[id] = out

	return out, nil
}

// hasMarkers reports whether a layer records any deletion.
func hasMarkers(dir string) (bool, error) {
	found := false

	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if !d.IsDir() && strings.HasPrefix(d.Name(), whPrefix) {
			found = true

			return fs.SkipAll
		}

		return nil
	})
	if err != nil {
		return false, fmt.Errorf("scan %s for deletions: %w", dir, err)
	}

	return found, nil
}

// translate copies a layer, turning its markers into what overlayfs reads.
func translate(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, p)
		if err != nil {
			return fmt.Errorf("relative path: %w", err)
		}

		target := filepath.Join(dst, rel)

		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o750) //nolint:wrapcheck // named by the caller

		case d.Name() == whOpaque:
			// The directory holding it replaces the one below, which overlayfs
			// reads as an attribute rather than as a file. The marker itself
			// must not survive into the merged view.
			//nolint:wrapcheck // named by the caller
			return unix.Lsetxattr(filepath.Dir(target), opaqueXattr(needsUserXattr(dst)), []byte("y"), 0)

		case strings.HasPrefix(d.Name(), whPrefix):
			// `.wh.<name>` means <name> was deleted: a character device 0:0
			// where the entry would be.
			// Through whiteoutTarget, because `TrimPrefix` alone let a layer
			// name the parent: `.wh...` strips to `..` and Join resolves it
			// outside the directory being translated (E630).
			name, err := whiteoutTarget(d.Name())
			if err != nil {
				return err
			}

			gone := filepath.Join(filepath.Dir(target), name)

			return unix.Mknod(gone, unix.S_IFCHR|0o600, 0) //nolint:wrapcheck // named by the caller

		default:
			// Hard-linked rather than copied: the source is read-only to the
			// step and its bytes are identical by construction, so a link is
			// both cheaper and impossible to disagree with. A cross-device
			// store falls back to a copy.
			err := os.Link(p, target)
			if err == nil {
				return nil
			}

			return copyOne(p, target)
		}
	})
}

// copyOne is the fallback when a layer and this VM's storage are not one
// filesystem, which is the ordinary case for a shared store.
func copyOne(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(src)
		if err != nil {
			return fmt.Errorf("read symlink %s: %w", src, err)
		}

		return os.Symlink(link, dst) //nolint:wrapcheck // named by the caller
	}

	in, err := os.Open(src) //nolint:gosec // a layer this engine wrote
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}

	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm()) //nolint:gosec // see above
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}

	defer out.Close()

	_, err = out.ReadFrom(in)
	if err != nil {
		return fmt.Errorf("copy %s: %w", src, err)
	}

	at := fi.ModTime()

	return os.Chtimes(dst, at, at) //nolint:wrapcheck // named by the caller
}

// unmarkedFile names the note that says a layer was scanned and carries no
// markers.
//
// Beside the translations rather than inside the layer: a layer is named by its
// content, and a file added to it would be a layer that is no longer what it
// says it is. The suffix cannot collide with a translation, whose name is a
// hex id.
func unmarkedFile(dir, id string) string { return UnmarkedNote(filepath.Join(dir, id)) }
