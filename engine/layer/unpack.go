package layer

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fstime"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// maxEntries and maxBody bound what a peer may make this engine allocate.
//
// A length is a number the *sender* chose. Without a bound, one four-byte field
// asks the receiver for four gigabytes, which is a denial of service that costs
// the attacker nothing to send.
const (
	maxEntries = 1 << 22
	maxBody    = 1 << 34 // 16 GiB: larger than any single file in a sane layer
)

// Unpack restores a packed layer into root.
//
// **Nothing here trusts the stream.** It arrived from a machine this one did not
// write (A5), so every path is resolved inside the root and refused if it
// escapes, every length is bounded, and a stream that ends early is an error
// rather than a smaller layer - half a layer is not a smaller layer, it is a
// tree whose digest is something nobody asked for.
//
// The caller verifies what it gets: `Take` on the result must equal the digest
// that was asked for. That check, not anything here, is what makes a layer from
// a peer safe to use as a base (§5.3), and it is why this function does not need
// to be clever about which fields matter - a field it restored wrongly shows up
// as a digest that does not match.
func Unpack(r io.Reader, root string) error {
	_, err := UnpackOwned(r, root)

	return err
}

// UnpackOwned is Unpack, and reports the ownership the stream declared.
//
// **The stream is the authority on who owns a layer's files, not the
// filesystem that received it.** Ownership is part of a layer's identity
// (§3.3) and `meta` can only *attempt* to restore it - an unprivileged worker
// cannot chown - so a capture that stats the result names a different layer on
// every machine whose user differs from the sender's. Two machines then cannot
// share a base at all, and say so as "the peer did not hold it" (E313).
//
// Returned rather than applied because there is nothing to apply it to: the
// files really are owned by whoever ran the unpack. What is recorded is what
// the layer *is*, which is the sender's declaration, checked the only way it
// can be - the digest that results must be the digest that was asked for.
//
// Keyed by the same slash-separated relative path the pack uses, so it lines up
// with a walk of the tree without either side normalising.
func UnpackOwned(r io.Reader, root string) (map[string]Owner, error) {
	d := &reader{r: r}

	if got := string(d.fixed(len(magic))); d.err == nil && got != magic {
		return nil, fmt.Errorf("%w: this is not a layer stream (magic %q)", ErrMalformed, got)
	}

	n := d.count(maxEntries)
	if d.err != nil {
		return nil, d.err
	}

	err := os.MkdirAll(root, 0o750)
	if err != nil {
		return nil, fmt.Errorf("make the layer root: %w", err)
	}

	ents := make([]packed, 0, min(n, 1<<16))

	for range n {
		e := d.entry()
		if d.err != nil {
			return nil, d.err
		}

		ents = append(ents, e)
	}

	bodies, err := d.bodies()
	if err != nil {
		return nil, err
	}

	// Directories and files first, hard links after: a link cannot be made to a
	// file that does not exist yet, and the stream is in path order rather than
	// dependency order.
	for _, e := range ents {
		err = restore(root, e, bodies)
		if err != nil {
			return nil, err
		}
	}

	for _, e := range ents {
		if e.kind == kindHardlink {
			err = link(root, e)
			if err != nil {
				return nil, err
			}
		}
	}

	// Times last, after everything that could disturb them. Creating a file
	// updates its directory's mtime and the digest includes that, so nothing may
	// be created after a stamp.
	//
	// Order among the stamps themselves does not matter, and an earlier version
	// of this loop ran in reverse on the theory that it did. Setting a child's
	// time does not touch its parent - only creating and removing do, and both
	// are finished by here. Mutation testing said so: reversing it back changed
	// nothing any test could see.
	for _, e := range ents {
		err = stamp(root, e)
		if err != nil {
			return nil, err
		}
	}

	owned := make(map[string]Owner, len(ents))
	for _, e := range ents {
		owned[e.path] = Owner{UID: e.uid, GID: e.gid}
	}

	return owned, nil
}

// Owner is who a layer says a path belongs to.
//
// Store terms, as every ownership in a layer is: the numbers the pack carries,
// not what any particular machine's filesystem was willing to record.
type Owner struct{ UID, GID uint32 }

// packed is one entry as it arrived.
type packed struct {
	path           string
	kind           byte
	mode, uid, gid uint32
	mtimeSec       int64
	mtimeNs        uint32
	size           int64
	content        ir.NodeID
	target         string
	xattrs         []xattr
}

// restore creates everything but hard links and timestamps.
func restore(root string, e packed, bodies map[ir.NodeID][]byte) error {
	p, err := safeJoin(root, e.path)
	if err != nil {
		return err
	}

	switch e.kind {
	case kindDir:
		err = os.MkdirAll(p, 0o700)

	case kindFile:
		body, ok := bodies[e.content]
		if !ok {
			return fmt.Errorf("%w: %s names contents %v that the stream does"+
				" not carry", ErrMalformed, e.path, e.content)
		}

		err = os.MkdirAll(filepath.Dir(p), 0o700)
		if err == nil {
			err = os.WriteFile(p, body, 0o600)
		}

	case kindSymlink:
		err = os.MkdirAll(filepath.Dir(p), 0o700)
		if err == nil {
			// The target is not resolved and not checked: a symlink may point
			// anywhere, including outside the layer, and that is a property of
			// the layer rather than an escape. Following one *while unpacking*
			// would be the escape, and nothing here follows.
			_ = os.Remove(p)
			err = os.Symlink(e.target, p)
		}

	case kindHardlink:
		return nil

	default:
		return fmt.Errorf("%w: %s has kind %q", ErrMalformed, e.path, e.kind)
	}

	if err != nil {
		return fmt.Errorf("restore %s: %w", e.path, err)
	}

	return meta(p, e)
}

// link makes a hard link once its target exists.
func link(root string, e packed) error {
	p, err := safeJoin(root, e.path)
	if err != nil {
		return err
	}

	at, err := safeJoin(root, e.target)
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Dir(p), 0o700)
	if err == nil {
		_ = os.Remove(p)
		err = os.Link(at, p)
	}

	if err != nil {
		return fmt.Errorf("link %s to %s: %w", e.path, e.target, err)
	}

	return nil
}

// meta restores mode, ownership and extended attributes.
//
// Ownership is *attempted*: setting it needs privilege, and a worker running
// unprivileged cannot. Failing here would refuse every honest layer on such a
// machine; the caller's digest check catches it instead, and says so in the one
// place that can tell the difference between "could not" and "did not need to".
func meta(p string, e packed) error {
	if e.kind != kindSymlink {
		err := os.Chmod(p, os.FileMode(e.mode).Perm())
		if err != nil {
			return fmt.Errorf("mode of %s: %w", p, err)
		}
	}

	_ = os.Lchown(p, int(e.uid), int(e.gid))

	return setXattrs(p, e.xattrs)
}

// stamp restores an entry's modification time.
func stamp(root string, e packed) error {
	p, err := safeJoin(root, e.path)
	if err != nil {
		return err
	}

	when := time.Unix(e.mtimeSec, int64(e.mtimeNs))

	// Without following: `os.Chtimes` on a symlink stamps its *target*, which
	// changes a file this layer also carries and leaves the link with whatever
	// time it was created - two wrong entries from one call. The digest includes
	// both, so the mistake is not subtle, but it is invisible until something
	// compares a restored layer with its own identity.
	if e.kind == kindSymlink {
		return fstime.Lchtimes(p, when, when)
	}

	err = os.Chtimes(p, when, when)
	if err != nil {
		return fmt.Errorf("times of %s: %w", e.path, err)
	}

	return nil
}

// reader decodes the stream, refusing rather than panicking.
type reader struct {
	r   io.Reader
	err error
}

func (d *reader) fixed(n int) []byte {
	if d.err != nil {
		return nil
	}

	b := make([]byte, n)

	_, err := io.ReadFull(d.r, b)
	if err != nil {
		d.err = fmt.Errorf("%w: wanted %d more bytes: %w", ErrMalformed, n, err)

		return nil
	}

	return b
}

func (d *reader) count(limit int) int {
	b := d.fixed(4)
	if d.err != nil {
		return 0
	}

	n := int(binary.BigEndian.Uint32(b))
	if n > limit {
		// Refused here rather than left to fail on the read that follows.
		// Every allocation from a count is already capped, so what this buys is
		// a *diagnosable* refusal: without it, a stream claiming a billion
		// entries fails as "wanted 4 more bytes" after reading everything it
		// had, and the person reading that message learns nothing about which
		// number was wrong or who chose it.
		d.err = fmt.Errorf("%w: a length of %d, over the bound of %d"+
			"\n  the sender chose this number", ErrMalformed, n, limit)

		return 0
	}

	return n
}

func (d *reader) str() string {
	n := d.count(1 << 20)

	return string(d.fixed(n))
}

func (d *reader) u32() uint32 {
	b := d.fixed(4)
	if d.err != nil {
		return 0
	}

	return binary.BigEndian.Uint32(b)
}

func (d *reader) i64() int64 {
	b := d.fixed(8)
	if d.err != nil {
		return 0
	}

	return int64(binary.BigEndian.Uint64(b)) //nolint:gosec // a size or an epoch second
}

func (d *reader) entry() packed {
	var e packed

	e.path = d.str()

	k := d.fixed(1)
	if d.err == nil {
		e.kind = k[0]
	}

	e.mode = d.u32()
	e.uid = d.u32()
	e.gid = d.u32()
	e.mtimeSec = d.i64()
	e.mtimeNs = d.u32()
	e.size = d.i64()

	if c := d.fixed(len(e.content)); d.err == nil {
		copy(e.content[:], c)
	}

	e.target = d.str()

	n := d.count(1 << 16)
	for range n {
		name, value := d.str(), d.str()
		if d.err != nil {
			break
		}

		e.xattrs = append(e.xattrs, xattr{name: name, value: value})
	}

	return e
}

func (d *reader) bodies() (map[ir.NodeID][]byte, error) {
	n := d.count(maxEntries)
	if d.err != nil {
		return nil, d.err
	}

	out := make(map[ir.NodeID][]byte, min(n, 1<<16))

	for range n {
		var id ir.NodeID

		if b := d.fixed(len(id)); d.err == nil {
			copy(id[:], b)
		}

		size := d.i64()
		if d.err == nil && (size < 0 || size > maxBody) {
			return nil, fmt.Errorf("%w: a body of %d bytes", ErrMalformed, size)
		}

		body := d.fixed(int(size))
		if d.err != nil {
			return nil, d.err
		}

		out[id] = body
	}

	return out, d.err
}
