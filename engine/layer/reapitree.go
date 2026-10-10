package layer

import (
	"io/fs"
	"slices"
	"strconv"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Tree is a stack as REAPI Directory messages, by digest.
//
// **One tree, in the other party's encoding.** An earlier version of this had
// two: a compact encoding of our own under a domain byte, and a REAPI one
// emitted beside it for anything that had to leave the machine. Two Merkle trees
// over one filesystem is two definitions of what a base *is*, and they agree
// until somebody edits one - which is the argument this repository makes about
// every other pair of encodings it has refused to keep.
//
// It is also smaller. Measured over a 4,000-entry tree, REAPI is 0.88x the
// compact encoding it replaced: a 64-character hex digest costs more than 32
// raw bytes, and omitting every default-valued field costs far less than a
// fixed-width block written whether or not anything is in it.
//
// What it gives up is stated in green paper 4.5b: injectivity now rests on
// protobuf framing, which no specification canonicalises, rather than on an
// encoding this document defines.
type Tree struct {
	root  ir.NodeID
	blobs map[ir.NodeID][]byte
}

// Root is 𝜏, the digest Κₜ keys on - and the input-root digest an REAPI Action
// would carry. They are the same number, which is the point of consolidating.
func (t Tree) Root() ir.NodeID { return t.root }

// Nodes is every Directory message in the tree, by digest.
func (t Tree) Nodes() map[ir.NodeID][]byte { return t.blobs }

// propPrefix namespaces what REAPI has no field for.
//
// NodeProperty is an untyped key/value with no registry behind it, so a name
// nobody owns is a name somebody else may use with another meaning.
const propPrefix = "earthbuild."

// Digest is 𝜏, the tree the fold has reached, and does not consume it.
//
// Only the directories a layer moved are encoded again; everything else answers
// from the digest it was given last time. That is the whole saving - a step
// writes tens of paths into a base of tens of thousands.
func (f *Fold) Digest() ir.NodeID {
	b := encoder{}
	id, _ := b.cached(f.root)

	return id
}

// Tree is the fold with every Directory's bytes kept, for shipping.
//
// Digest keeps only names; this keeps the messages a peer would ask for. A key
// needs the name and a transfer needs the encoding, and holding the encodings
// for every fold in a memo is not a cost a key should carry.
func (f *Fold) Tree() Tree {
	b := encoder{blobs: map[ir.NodeID][]byte{}}
	t := Tree{blobs: b.blobs}
	t.root, _ = b.walk(f.root)

	return t
}

// rootDigestOf is 𝜏 of a set with no fold behind it. See capture.
func rootDigestOf(merged map[string]entry) ir.NodeID {
	b := encoder{}
	id, _ := b.walk(rootOf(merged))

	return id
}

// encoder writes directories, optionally keeping each one's bytes.
type encoder struct {
	blobs map[ir.NodeID][]byte
	sc    scratch
}

// cached names a directory, reusing what is still true beneath it.
//
// **The node is the unit of reuse in both directions.** A subtree no layer
// touched keeps its name, which is what makes the digest incremental here and
// what lets a peer skip fetching it there - one property, read twice.
func (b *encoder) cached(d *dir) (ir.NodeID, int64) {
	if d.clean {
		return d.digest, d.size
	}

	d.digest, d.size = b.emit(d, b.cached)
	d.clean = true

	return d.digest, d.size
}

// walk names every directory without consulting or setting the cache.
func (b *encoder) walk(d *dir) (ir.NodeID, int64) { return b.emit(d, b.walk) }

// emit writes one directory's Directory message and names it.
//
// Child digests come from the caller, which is the only difference between
// naming a whole tree and naming what a layer changed - the encoding itself is
// written once, here, so the two cannot drift.
//
// A directory's own metadata is not in its own message. REAPI keeps it in
// `Directory.node_properties`; this engine keeps it in the parent's entry for
// the directory, so a subtree holds one name however the directory above it is
// permissioned - which is the reuse the tier is for. The root has no parent and
// so no metadata anywhere, which is correct: a stack's root is the mount point
// and not something the layers describe.
func (b *encoder) emit(d *dir, child func(*dir) (ir.NodeID, int64)) (ir.NodeID, int64) {
	names := make([]string, 0, len(d.files))
	for n := range d.files {
		names = append(names, n)
	}

	slices.Sort(names) // REAPI requires each list in name order

	var (
		files []reapiFile
		links []reapiSymlink
	)

	for _, n := range names {
		e := d.files[n]
		if kindOf(e.mode) == 'l' {
			links = append(links, reapiSymlink{
				name: n, target: e.link, props: propertiesOf(e, true),
			})

			continue
		}

		files = append(files, reapiFile{
			name:       n,
			hash:       e.content,
			size:       e.size,
			executable: e.mode&0o111 != 0,
			props:      propertiesOf(e, false),
		})
	}

	subs := make([]string, 0, len(d.subdirs))
	for n := range d.subdirs {
		subs = append(subs, n)
	}

	slices.Sort(subs)

	dirs := make([]reapiDir, 0, len(subs))

	for _, n := range subs {
		id, size := child(d.subdirs[n])
		dirs = append(dirs, reapiDir{name: n, hash: id, size: size})
	}

	own := []reapiProperty(nil)
	if d.own != nil {
		own = propertiesOf(*d.own, false)
	}

	enc := encodeDirectory(&b.sc, files, dirs, links, own)
	id := ir.DigestOf(enc)

	if b.blobs != nil {
		b.blobs[id] = enc
	}

	return id, int64(len(enc))
}

// propertiesOf is everything about an entry REAPI has no field for.
//
// **Empty for an ordinary entry, which is the point.** REAPI does not model
// ownership at all - a worker materialises as itself - and 644/755 is exactly
// `is_executable`. So a root-owned tree of ordinary files says nothing, the
// field is omitted, and the bytes are what Bazel would have produced. A tree
// emitting `uid: 0` on every file would agree with nobody.
//
// A kind REAPI has no node for - a device, a named pipe - is carried here rather
// than refused. The refusal belongs where a tree is *handed to* a remote
// execution service, not where it is named: a conforming consumer would
// materialise an empty regular file, and nothing but us ever reads these unless
// we choose to send them.
func propertiesOf(e entry, isLink bool) []reapiProperty {
	var out []reapiProperty

	add := func(name, value string) {
		out = append(out, reapiProperty{propPrefix + name, value})
	}

	if k := kindOf(e.mode); k != 'f' && k != 'd' && k != 'l' {
		add("kind", describeKind(e.mode))

		if e.rdev != 0 {
			add("rdev", strconv.FormatUint(e.rdev, 10))
		}
	}

	if e.uid != 0 {
		add("uid", strconv.FormatUint(uint64(e.uid), 10))
	}

	if e.gid != 0 {
		add("gid", strconv.FormatUint(uint64(e.gid), 10))
	}

	// A symlink's mode is an invention the platforms disagree about, normalised
	// by hashedMode and meaningless to REAPI either way.
	if perm := e.mode & 0o7777; !isLink && !ordinaryMode(perm) {
		add("mode", "0"+strconv.FormatUint(uint64(perm), 8))
	}

	if e.hardlink != "" {
		add("hardlink", e.hardlink)
	}

	for _, x := range e.xattrs {
		add("xattr."+x.name, x.value)
	}

	slices.SortFunc(out, func(a, b reapiProperty) int {
		switch {
		case a.name < b.name:
			return -1
		case a.name > b.name:
			return 1
		default:
			return 0
		}
	})

	return out
}

// ordinaryMode is a mode `is_executable` already says.
func ordinaryMode(perm uint32) bool { return perm == 0o644 || perm == 0o755 }

// describeKind names a node type REAPI has no message for.
func describeKind(mode uint32) string {
	switch kindOf(mode) {
	case 'b':
		if fs.FileMode(mode)&fs.ModeCharDevice != 0 {
			return "chardev"
		}

		return "blockdev"
	case 'p':
		return "fifo"
	case 's':
		return "socket"
	default:
		return "unknown"
	}
}
