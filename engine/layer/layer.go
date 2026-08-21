// Package layer captures a filesystem tree as a layer identity.
//
// A layer's digest is what the action cache stores and what a fleet worker
// transfers, so two properties are load-bearing. It must be **deterministic** -
// the same tree on two machines yields the same digest, or the cache is a
// lottery (I1). And it must be **complete** with respect to green paper §3.3 -
// anything a step can observe about a file and that this function ignores is a
// difference two layers can carry while claiming to be the same, which is a
// false cache hit rather than a rounding error.
package layer

import (
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Capture is what a tree yielded: two digests over the same walk.
//
// The pair exists because "what do I restore?" and "did this step behave
// deterministically?" are different questions, and one digest cannot answer
// both. Creating a directory stamps it with the wall clock, so two runs of an
// identical step produce different identities - faithful, and useless as a
// determinism screen.
type Capture struct {
	// ID is the layer's identity: every field of green paper §3.3, timestamps
	// included. This is what 𝔄 stores and what a restore must reproduce.
	ID ir.NodeID
	// Content answers "the same bytes and structure?" - identical to ID except
	// that mtimes are excluded. Determinism screening (§6) compares this, so a
	// step is judged on what it produced rather than on when it ran.
	Content ir.NodeID
	// Bytes is the total size of file contents, for the scheduler's cost model:
	// a fleet scheduler that estimates time but not bytes places work as though
	// transfers were free.
	Bytes int64
}

// Take captures the tree at root.
func Take(root string) (Capture, error) { return TakeIn(root, IDMap{}, IDMap{}) }

// TakeIn is Take with ownership translated as a namespace sees it.
//
// A layer's identity is always in **store terms**, whoever computes it. Two
// places do: the guest, which captures a step's delta inside its user
// namespace, and `LayerStore.Verify`, which recomputes the digest on the host
// to authenticate a layer arriving from outside the trust domain (§5.3).
//
// Ownership is part of what a layer records (§3.3, E92), and the two read the
// same bytes through different mappings - a file a step made as root is uid 0
// to the guest and the invoking user to the host. Without translating, the host
// recomputes a different digest and **rejects every honest layer**: latent
// today because `Verify` has no caller until the fleet transport exists, and
// fires on S6's first day (E135).
//
// The zero maps are the identity, which is what a host-side capture wants.
func TakeIn(root string, uids, gids IDMap) (Capture, error) {
	return TakeOwnedIn(root, uids, gids, nil)
}

// TakeOwnedIn is TakeIn with ownership taken from a declaration, not the disk.
//
// **For a tree this machine restored rather than made.** An unprivileged unpack
// cannot chown, so every file lands owned by whoever ran it - and a capture that
// stats the result names a layer nobody sent. `UnpackOwned` reports what the
// stream declared and this hashes that instead, which is what makes a base
// transferable between two machines with different users at all (E313).
//
// A nil map is a tree this machine made, where the disk *is* the authority. A
// path the map does not mention falls back the same way: a declaration that
// covers some of a tree is a declaration about those paths, not a licence to
// invent ownership for the rest.
//
// The IDMap translation still applies on top, and in that order: the
// declaration is in the sender's store terms, and the maps say how this
// namespace's terms relate to the store's.
func TakeOwnedIn(
	root string, uids, gids IDMap, own map[string]Owner,
) (Capture, error) {
	entries, size, err := walk(root)
	if err != nil {
		return Capture{}, err
	}

	return capture(declared(entries, own), size, uids, gids), nil
}

// declared applies a layer's own account of who owns it.
//
// **One place, because there are three walks and they must agree.** The
// capture, the pack and the manifest all hash ownership, and a store whose
// three disagreed would file a layer under a digest it could not reproduce and
// prove a fragment against a layer nobody has.
//
// A path the declaration does not mention keeps what the disk says. A partial
// declaration is a statement about the paths it names, not a licence to invent
// ownership for the rest - which is what a fragment's is: it covers the files
// that came with it and nothing else.
func declared(entries []entry, own map[string]Owner) []entry {
	if len(own) == 0 {
		return entries
	}

	for i := range entries {
		if o, ok := own[entries[i].path]; ok {
			entries[i].uid, entries[i].gid = o.UID, o.GID
		}
	}

	return entries
}

// capture hashes a walked tree, which is the half TakeIn and TakeExcluding
// share.
//
// One function, because a second place that sorted and hashed entries would be a
// second definition of what a layer *is* - and the two would agree until
// somebody edited one.
func capture(entries []entry, size int64, uids, gids IDMap) Capture {
	// Sorted, because directory iteration order is a property of the filesystem
	// and must not reach the digest. Paths are compared as byte strings, which
	// is locale-independent - a collation-aware sort would make identity depend
	// on the machine's locale.
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	full, content := ir.NewHasher(), ir.NewHasher()

	full.Count(len(entries))
	content.Count(len(entries))

	for _, e := range entries {
		// Translated before hashing, so the digest is the one the store would
		// produce rather than the one this namespace happens to see.
		e.uid = uids.Outside(e.uid)
		e.gid = gids.Outside(e.gid)

		e.hash(&full.Encoder, withTimes)
		e.hash(&content.Encoder, withoutTimes)
	}

	return Capture{ID: full.Sum(), Content: content.Sum(), Bytes: size}
}

// Digest returns just the layer identity and size.
func Digest(root string) (ir.NodeID, int64, error) {
	c, err := Take(root)

	return c.ID, c.Bytes, err
}

// entry is one path's captured metadata: green paper §3.3, in full.
//
// Deliberately absent: atime and ctime. Reading a file changes its atime, so
// including it would make a layer's identity depend on who last read the source
// tree - a cache that misses because something looked at it.
type entry struct {
	path     string
	mode     uint32
	uid, gid uint32
	mtimeSec int64
	mtimeNs  uint32
	size     int64
	content  ir.NodeID // regular files only
	link     string    // symlinks only
	rdev     uint64    // device nodes only
	hardlink string    // the first path sharing this inode, if any
	xattrs   []xattr
}

type xattr struct{ name, value string }

// hash writes the entry into the injective encoding of green paper §1.4.
//
// Fixed-width fields go in raw; variable-width ones are length-prefixed. The
// kind byte comes first so that a symlink named "x" and a regular file named
// "x" cannot produce the same bytes by coincidence of their other fields.
// times selects whether mtimes enter a digest. See Capture.
type times bool

const (
	withTimes    times = true
	withoutTimes times = false
)

func (e entry) hash(h *ir.Encoder, t times) {
	h.Str(e.path)
	h.Byte(kindOf(e.mode))

	var fixed [4 + 4 + 4 + 8 + 4 + 8 + 8]byte

	binary.BigEndian.PutUint32(fixed[0:], e.mode)
	binary.BigEndian.PutUint32(fixed[4:], e.uid)
	binary.BigEndian.PutUint32(fixed[8:], e.gid)
	if t == withTimes {
		binary.BigEndian.PutUint64(fixed[12:], uint64(e.mtimeSec)) //nolint:gosec // two's complement round-trips
		binary.BigEndian.PutUint32(fixed[20:], e.mtimeNs)
	}
	binary.BigEndian.PutUint64(fixed[24:], uint64(e.size)) //nolint:gosec // never negative
	binary.BigEndian.PutUint64(fixed[32:], e.rdev)
	h.Fixed(fixed[:])

	// A digest is fixed-width by §3.1, so it needs no prefix. The variable
	// fields around it do.
	h.Fixed(e.content[:])
	h.Str(e.link)
	h.Str(e.hardlink)

	h.Count(len(e.xattrs))

	for _, x := range e.xattrs {
		h.Str(x.name)
		h.Str(x.value)
	}
}

// kindOf reduces the mode to its type, so the type is hashed even where a
// platform reports mode bits differently.
func kindOf(mode uint32) byte {
	switch {
	case fs.FileMode(mode)&fs.ModeSymlink != 0:
		return 'l'
	case fs.FileMode(mode).IsDir():
		return 'd'
	case fs.FileMode(mode)&fs.ModeDevice != 0:
		return 'b'
	case fs.FileMode(mode)&fs.ModeNamedPipe != 0:
		return 'p'
	case fs.FileMode(mode)&fs.ModeSocket != 0:
		return 's'
	default:
		return 'f'
	}
}

func walk(root string) ([]entry, int64, error) { return walkNeeding(root, true) }

// walkNeeding is walk, optionally without reading any file's contents.
//
// **A pack of part of a layer does not need the rest of it read.** Every caller
// but one wants the digests: a capture is the layer's identity and a manifest
// describes every path, so both need all of them. A pack given a path list needs
// the contents of what it is sending and the metadata of what it is scaffolding,
// and hashing the remainder made a fragment cost the size of its layer - twenty
// times over, for one file of a 400-file tree (E338).
//
// The contents of what survives the filter are filled in afterwards, by
// `fillContents`, which is the only place that knows what survived.
func walkNeeding(root string, contents bool) ([]entry, int64, error) {
	// A root that is itself a file is handled whole by walkOne, contents
	// included, and its entry is named by its basename rather than by a path
	// under the root - so filling it again would look for `f/f`. The single-file
	// case is the one where the split below does not apply.
	if fi, err := os.Lstat(root); err == nil && !fi.IsDir() {
		return walkOne(root)
	}

	entries, size, err := walkMetadata(root)
	if err != nil || !contents {
		return entries, size, err
	}

	err = fillContents(root, entries)
	if err != nil {
		return nil, 0, err
	}

	return entries, size, nil
}

// walkMetadata is the ordered half: every entry, without any file's contents.
func walkMetadata(root string) ([]entry, int64, error) {
	// A root that is itself a file is digested as one entry named by its base.
	// The walk below skips its own root - correct for a directory, where the root
	// is the layer rather than a member of it - which for a file meant hashing
	// nothing at all, so every file shared one identity.
	fi, err := os.Lstat(root)
	if err == nil && !fi.IsDir() {
		return walkOne(root)
	}

	var (
		entries []entry
		size    int64
		// inodes maps an inode to the first path that claimed it, which is how
		// hardlink identity is captured: two paths sharing an inode are not two
		// independent copies, and a layer that recorded them as such would lose
		// the link on restore.
		inodes = map[uint64]string{}
	)

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return fmt.Errorf("relative path of %s: %w", p, err)
		}

		if rel == "." {
			return nil // the root itself is the layer, not a member of it
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", p, err)
		}

		e := entry{
			path:     filepath.ToSlash(rel),
			mode:     uint32(info.Mode()),
			mtimeSec: info.ModTime().Unix(),
			mtimeNs:  uint32(info.ModTime().Nanosecond()), //nolint:gosec // < 1e9
		}

		platformMeta(&e, info, inodes)

		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return fmt.Errorf("read symlink %s: %w", p, err)
			}

			// The target string, never what it points at: following it would
			// make this layer's identity depend on a tree outside it.
			e.link = target

		case info.Mode().IsRegular():
			e.size = info.Size()
			size += info.Size()

			// Contents are not read here even when they are wanted. The walk is
			// ordered and one file at a time; hashing is neither, and it is
			// where the time goes - a capture of a 267MB base image spent 1.98s
			// of its 2s in this one line, on one core of however many the
			// machine has. See fillContents.
		}

		xs, err := readXattrs(p)
		if err == nil {
			e.xattrs = xs
		}

		entries = append(entries, e)

		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("capture %s: %w", root, err)
	}

	return entries, size, nil
}

// walkOne captures a single file as a one-entry layer.
func walkOne(p string) ([]entry, int64, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, 0, fmt.Errorf("stat %s: %w", p, err)
	}

	e := entry{
		path:     filepath.Base(p),
		mode:     uint32(fi.Mode()),
		mtimeSec: fi.ModTime().Unix(),
		mtimeNs:  uint32(fi.ModTime().Nanosecond()), //nolint:gosec // < 1e9
	}

	platformMeta(&e, fi, map[uint64]string{})

	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(p)
		if err != nil {
			return nil, 0, fmt.Errorf("read symlink %s: %w", p, err)
		}

		e.link = target

	case fi.Mode().IsRegular():
		e.size = fi.Size()

		e.content, err = contentDigest(p)
		if err != nil {
			return nil, 0, err
		}
	}

	xs, err := readXattrs(p)
	if err == nil {
		e.xattrs = xs
	}

	return []entry{e}, e.size, nil
}

// digested counts the files whose contents have been read.
//
// **Because the property is how many files are read, not how long that takes.**
// The first test of E338 compared the time to pack one file out of a small layer
// against the same file out of a large one - which is a ratio of two clocks, and
// tripped its own bound under load while being a factor of five clear when run
// alone. A count is exact, fast, and says the thing (E350).
var digested atomic.Int64

// DigestedForTest is how many files this package has read the contents of.
//
// Exported for a test in this package's own directory rather than a seam a
// caller could set: nothing is injected, so nothing can be got wrong by it.
func DigestedForTest() int64 { return digested.Load() }

func contentDigest(p string) (ir.NodeID, error) {
	digested.Add(1)

	f, err := os.Open(p) //nolint:gosec // the path came from walking the tree
	if err != nil {
		return ir.NodeID{}, fmt.Errorf("open %s: %w", p, err)
	}

	defer f.Close()

	h := ir.NewHasher()

	// Streamed rather than read whole: a layer may contain a file larger than
	// the machine's memory, and a capture that dies on one is a capture that
	// works until it matters.
	_, err = io.Copy(h, f)
	if err != nil {
		return ir.NodeID{}, fmt.Errorf("read %s: %w", p, err)
	}

	return h.Sum(), nil
}

// ObservedOwnerForTest makes a walk report ownership other than the disk's, and
// puts it back when the test ends.
//
// In a normal file rather than an `_test.go` one because the tests that need it
// are in `engine/fleet`: E313 is a fault of the *store*, and reproducing it
// where it bit is the point. Named as this repo names its other seams.
//
// A test that swaps this cannot be parallel - it is a package variable, and what
// it races against is every other test that captures a tree. Enforced below.
func ObservedOwnerForTest(t *testing.T, fn func(uid, gid uint32) (uint32, uint32)) {
	t.Helper()

	// **The rule, enforced rather than written down.** `t.Setenv` fails a test
	// that has called `t.Parallel`, which is exactly the condition a package
	// variable cannot survive - and saying so in a comment was not enough: two
	// of the first three tests to use this seam were parallel, and one of them
	// corrupted an unrelated symlink test that only failed in a full run.
	t.Setenv("EARTHBUILD_LAYER_SEAM", "1")

	was := observedOwner
	observedOwner = fn

	t.Cleanup(func() { observedOwner = was })
}

// fillContents reads the files these entries name.
//
// The second half of a walk that skipped them. Only regular files have contents;
// a directory carried along to scaffold a fragment has none, and asking for one
// would read a directory as a file.
func fillContents(root string, entries []entry) error {
	// Hashing is CPU-bound and every file is independent of every other: each
	// worker writes to its own index of a slice that is already the right
	// length, so nothing is shared and nothing needs ordering. A capture of a
	// 267MB base image spent 1.98 seconds reading files on a single core, and
	// every RUN in every build pays a capture.
	//
	// Bounded by CPU count. The work is hashing rather than waiting, so more
	// goroutines than cores buys nothing and costs scheduling.
	workers := min(runtime.NumCPU(), len(entries))
	if workers < 2 {
		return fillRange(root, entries, 0, len(entries))
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		bad  error
		next atomic.Int64
	)

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			// Claimed one at a time rather than sliced into equal parts: a
			// layer's files vary in size by orders of magnitude, and a fixed
			// split leaves one worker holding every large file while the rest
			// finish early.
			for {
				i := int(next.Add(1)) - 1
				if i >= len(entries) {
					return
				}

				err := fillRange(root, entries, i, i+1)
				if err != nil {
					mu.Lock()

					if bad == nil {
						bad = err
					}

					mu.Unlock()

					return
				}
			}
		}()
	}

	wg.Wait()

	return bad
}

// fillRange reads the contents of entries[from:to].
func fillRange(root string, entries []entry, from, to int) error {
	for i := from; i < to; i++ {
		if !fs.FileMode(entries[i].mode).IsRegular() {
			continue
		}

		d, err := contentDigest(filepath.Join(root, filepath.FromSlash(entries[i].path)))
		if err != nil {
			return err
		}

		entries[i].content = d
	}

	return nil
}
