package layer

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// magic names the format and its version, so a stream from a future engine is
// refused rather than misread. Eight bytes, fixed width, first.
const magic = "EBLAYER1"

// ErrMalformed marks a stream this engine will not act on.
var ErrMalformed = errors.New("malformed layer stream")

// kinds, one byte each. The byte is written before anything variable so that
// two entries differing only in kind cannot encode alike.
const (
	kindDir      = 'd'
	kindFile     = 'f'
	kindSymlink  = 'l'
	kindHardlink = 'h'
)

// Pack writes a layer as a deterministic byte stream.
//
// The piece the fleet was missing (E261): a layer is a *directory* and the
// transfer protocol moves *bytes*, with no conversion between them, so a worker
// could be told a base's digest and had no way to obtain it.
//
// **Deterministic, and that is the whole requirement.** Two machines packing one
// layer must produce identical bytes, or the fleet holds as many copies as it
// has senders and none of them share a cache entry - the transfer would grow the
// cache instead of using it. So this walks and sorts exactly as `TakeIn` does,
// by the same byte-string comparison, and every field is written at a fixed
// width or with a length prefix (§1.4).
//
// Contents are written **once per distinct digest**, after the entries. A layer
// with a hundred copies of one file - a licence, a header, a vendored module -
// then costs one copy on the wire, and the order is by digest so that it too is
// a property of the layer rather than of the walk.
func Pack(root string, w io.Writer) error { return PackPaths(root, w, nil) }

// PackPaths writes the part of a layer that somebody asked for.
//
// Most of a base is never read. A container runtime answers that with a seekable
// layer format and learns what is needed by watching a workload fault; this
// engine already **knows**, because §3.4 records what a step read and Κ₂ turns
// that into a prediction of what it will read again (E281).
//
// The ancestors of every wanted path come too, and not as a convenience: a file
// cannot be placed without the directories above it, and those directories carry
// modes and ownership the step will see.
//
// **A fragment is not the layer.** A layer is named by the digest of its whole
// tree (§3.2), so what this produces captures to a different identity and must
// never be filed as the layer - it is a materialisation strategy, not a
// different layer, and a store that confused the two would serve a fragment to
// every later build as though it were the base.
//
// Nil `want` is everything, and is byte-for-byte what `Pack` produces. That has
// to hold: two encodings of one tree is the determinism problem E262 exists to
// avoid.
//
// A wanted path the layer does not have is ordinary and not an error. The paths
// are a *prediction* (I5), and a prediction that names something absent is a step
// that looked and did not find - refusing would turn a hint into a requirement.
func PackPaths(root string, w io.Writer, want []string) error {
	return PackOwned(root, w, want, nil)
}

// PackOwned is PackPaths with ownership taken from a declaration, not the disk.
//
// **The relay half of E313.** A worker stores a layer it could not chown, so
// the tree on its disk is owned by whoever runs the worker. Packing that walk
// would declare *this* machine's user to the next one, which would file the
// result under a digest nobody asked for - so only the machine that originally
// made a layer could ever serve it, and C.4's mesh collapses into a star.
//
// The pair with `TakeOwnedIn`, and it has to be a pair: a store that captures
// against a declaration and packs against the disk holds a layer whose identity
// it cannot reproduce.
func PackOwned(root string, w io.Writer, want []string, own map[string]Owner) error {
	// **Metadata first, contents after the filter.** A pack of a whole layer
	// needs every file read; a pack of part of one needs the part. Reading the
	// rest made a fragment cost the size of the layer it came from (E338).
	entries, _, err := walkNeeding(root, len(want) == 0)
	if err != nil {
		return fmt.Errorf("read the layer at %s: %w", root, err)
	}

	entries = declared(entries, own)

	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	entries = keeping(entries, want)

	if len(want) > 0 {
		if err := fillContents(root, entries); err != nil {
			return fmt.Errorf("read the layer at %s: %w", root, err)
		}
	}

	e := ir.NewEncoder(w)

	e.Fixed([]byte(magic))
	e.Count(len(entries))

	bodies := map[ir.NodeID]string{}

	for _, en := range entries {
		k, err := kindByte(en)
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Join(root, en.path), err)
		}

		writeEntry(e, en, k)

		if k == kindFile {
			bodies[en.content] = filepath.Join(root, en.path)
		}
	}

	ids := make([]ir.NodeID, 0, len(bodies))
	for id := range bodies {
		ids = append(ids, id)
	}

	// By digest, so the order is a property of the layer and not of a map.
	sort.Slice(ids, func(i, j int) bool {
		return string(ids[i][:]) < string(ids[j][:])
	})

	e.Count(len(ids))

	for _, id := range ids {
		body, err := os.ReadFile(bodies[id]) //nolint:gosec // a path this walk produced
		if err != nil {
			return fmt.Errorf("read %s: %w", bodies[id], err)
		}

		e.Fixed(id[:])
		e.Fixed(be64(int64(len(body))))
		e.Fixed(body)
	}

	return nil
}

// kindByte says how an entry is carried, refusing what cannot be restored.
//
// A device node is refused by name rather than skipped: creating one needs
// privileges the receiver may not have, and a layer silently missing one would
// restore to a *different* digest - which is a false cache hit dressed as a
// successful transfer.
func kindByte(e entry) (byte, error) {
	switch {
	case e.hardlink != "":
		return kindHardlink, nil
	case e.link != "":
		return kindSymlink, nil
	case kindOf(e.mode) == 'd':
		return kindDir, nil
	case kindOf(e.mode) == 'f':
		return kindFile, nil
	}

	return 0, fmt.Errorf("%w: this engine can pack directories, regular files,"+
		" symlinks and hard links, and this is none of them (mode %#o)"+
		"\n  a device node or socket cannot be restored without privileges the"+
		" receiver may not have, and one silently dropped would change the"+
		" layer's identity", ErrMalformed, e.mode)
}

func writeEntry(e *ir.Encoder, en entry, k byte) {
	e.Str(en.path)
	e.Byte(k)
	e.Fixed(be32(en.mode))
	e.Fixed(be32(en.uid))
	e.Fixed(be32(en.gid))
	e.Fixed(be64(en.mtimeSec))
	e.Fixed(be32(en.mtimeNs))
	e.Fixed(be64(en.size))
	e.Fixed(en.content[:])

	target := en.link
	if k == kindHardlink {
		target = en.hardlink
	}

	e.Str(target)
	e.Count(len(en.xattrs))

	for _, x := range en.xattrs {
		e.Str(x.name)
		e.Str(x.value)
	}
}

func be32(v uint32) []byte {
	var b [4]byte

	binary.BigEndian.PutUint32(b[:], v)

	return b[:]
}

func be64(v int64) []byte {
	var b [8]byte

	binary.BigEndian.PutUint64(b[:], uint64(v)) //nolint:gosec // a size or an epoch second

	return b[:]
}

// safeJoin resolves an entry's path inside root, refusing one that escapes.
//
// The stream came from a peer (A5). An entry named `../../etc/profile`, written
// where it asked, would let any machine in a fleet write anywhere on any other -
// which is the most valuable thing a build system can be made to do for an
// attacker, and it is one missing check away.
//
// Checked on the *cleaned* path rather than by looking for `..`: `a/../../x`
// contains no leading `..` and escapes anyway.
func safeJoin(root, p string) (string, error) {
	if p == "" || strings.HasPrefix(p, "/") || filepath.IsAbs(p) {
		return "", fmt.Errorf("%w: entry path %q is absolute", ErrMalformed, p)
	}

	full := filepath.Clean(filepath.Join(root, p))

	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: entry path %q escapes the layer root", ErrMalformed, p)
	}

	return full, nil
}

// keeping is the entries a fragment carries: what was asked for, and the
// directories above it.
//
// Everything when nothing was asked for, which is what makes `Pack` the
// degenerate case of this rather than a second implementation.
func keeping(entries []entry, want []string) []entry {
	if len(want) == 0 {
		return entries
	}

	// Two sets, and the difference is the whole of getting this right. A
	// directory **asked for** brings what is inside it: a step that read a
	// directory read what was in it, and sending the directory alone would be
	// the shape of an answer without the answer. A directory kept only to hold
	// something below it brings nothing of its own - it is scaffolding, and
	// treating the two alike sends a wanted file's every sibling.
	asked := make(map[string]bool, len(want))
	scaffold := make(map[string]bool, len(want)*4)

	for _, p := range want {
		p = strings.TrimPrefix(filepath.Clean(p), "/")
		if p == "." || p == "" {
			continue
		}

		asked[p] = true

		for d := filepath.Dir(p); d != "." && d != "" && d != "/"; d = filepath.Dir(d) {
			scaffold[d] = true
		}
	}

	out := entries[:0]

	for _, en := range entries {
		if asked[en.path] || scaffold[en.path] || under(en.path, asked) {
			out = append(out, en)
		}
	}

	return out
}

// under reports whether a path lies inside a directory that was asked for.
func under(path string, asked map[string]bool) bool {
	for p := filepath.Dir(path); p != "." && p != "" && p != "/"; p = filepath.Dir(p) {
		if asked[p] {
			return true
		}
	}

	return false
}
