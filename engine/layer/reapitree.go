package layer

import (
	"fmt"
	"io/fs"
	"slices"
	"strconv"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// REAPITree is a stack as REAPI Directory blobs, by digest.
//
// The same tree 𝜈 (green paper 4.5b) names, written in the other party's
// encoding. Both are Merkle trees over directories and both name a subtree by
// what is under it; they differ in what they can say about an entry, which is
// what NodeProperties is for.
type REAPITree struct {
	root  ir.NodeID
	blobs map[ir.NodeID][]byte
}

// Root is the input-root digest an Action would carry.
func (t REAPITree) Root() ir.NodeID { return t.root }

// Blobs is every Directory message in the tree, by digest. A peer asks for the
// ones it lacks and verifies each against the name it asked for.
func (t REAPITree) Blobs() map[ir.NodeID][]byte { return t.blobs }

// propPrefix namespaces what REAPI has no field for.
//
// NodeProperty is an untyped key/value with no registry behind it, so a name
// nobody owns is a name somebody else may also use with a different meaning.
const propPrefix = "earthbuild."

// REAPI is this fold as REAPI Directory messages.
//
// **Digested with ℋ, which is the only coherent choice.** REAPI fixes one digest
// function per conversation, and the file digests in these messages are the ones
// the store already holds - so the directories have to be named by the same
// function or the tree is self-inconsistent. Under a SHA-256 store that is
// SHA-256 and the result is what any REAPI consumer expects; under BLAKE3 it is
// `DigestFunction.BLAKE3` (9), which Bazel accepts and Buck2 does not.
//
// An error where the tree holds something REAPI has no message for. Refused
// rather than approximated: there are FileNode, DirectoryNode and SymlinkNode
// and nothing else, so a device emitted as a FileNode would have a conforming
// consumer materialise an empty regular file where a device belongs, and
// nothing anywhere would say so.
func (f *Fold) REAPI() (REAPITree, error) {
	t := REAPITree{blobs: map[ir.NodeID][]byte{}}

	root, _, err := t.encode(f.root, "")
	if err != nil {
		return REAPITree{}, err
	}

	t.root = root

	return t, nil
}

// encode writes one directory and returns its digest and serialised size.
//
// `at` is carried only to name a path in a refusal: nothing about where a
// directory sits reaches its digest, which is what lets two bases share it.
func (t REAPITree) encode(d *dir, at string) (ir.NodeID, int64, error) {
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

		switch kindOf(e.mode) {
		case 'f':
			files = append(files, reapiFile{
				name:       n,
				hash:       e.content.String(),
				size:       e.size,
				executable: e.mode&0o111 != 0,
				props:      propertiesOf(e, false),
			})

		case 'l':
			links = append(links, reapiSymlink{
				name: n, target: e.link, props: propertiesOf(e, true),
			})

		default:
			return ir.NodeID{}, 0, fmt.Errorf(
				"%s is %s, and the remote execution API has no message for it"+
					"\n  a Directory holds files, directories and symlinks, and nothing else"+
					"\n  emitting it as a file would have the other side create an empty"+
					" regular file where this belongs",
				join(at, n), describeKind(e.mode))
		}
	}

	subs := make([]string, 0, len(d.subdirs))
	for n := range d.subdirs {
		subs = append(subs, n)
	}

	slices.Sort(subs)

	dirs := make([]reapiDir, 0, len(subs))

	for _, n := range subs {
		id, size, err := t.encode(d.subdirs[n], join(at, n))
		if err != nil {
			return ir.NodeID{}, 0, err
		}

		dirs = append(dirs, reapiDir{name: n, hash: id.String(), size: size})
	}

	b := encodeDirectory(files, dirs, links)
	id := ir.DigestOf(b)
	t.blobs[id] = b

	return id, int64(len(b)), nil
}

// propertiesOf is everything about an entry REAPI has no field for.
//
// **Empty for an ordinary entry, which is the point.** REAPI does not model
// ownership at all - a worker materialises as itself - and 644/755 is exactly
// `is_executable`. So a root-owned tree of ordinary files says nothing, the
// field is omitted, and our bytes are what Bazel would have produced. A tree
// that emitted `uid: 0` on every file would agree with nobody.
func propertiesOf(e entry, isLink bool) []reapiProperty {
	var out []reapiProperty

	if e.uid != 0 {
		out = append(out, reapiProperty{propPrefix + "uid", strconv.FormatUint(uint64(e.uid), 10)})
	}

	if e.gid != 0 {
		out = append(out, reapiProperty{propPrefix + "gid", strconv.FormatUint(uint64(e.gid), 10)})
	}

	// A symlink's mode is an invention the platforms disagree about, normalised
	// to 0777 by hashedMode and meaningless to REAPI either way.
	if perm := e.mode & 0o7777; !isLink && !ordinaryMode(perm) {
		out = append(out, reapiProperty{propPrefix + "mode", "0" + strconv.FormatUint(uint64(perm), 8)})
	}

	if e.hardlink != "" {
		out = append(out, reapiProperty{propPrefix + "hardlink", e.hardlink})
	}

	for _, x := range e.xattrs {
		out = append(out, reapiProperty{propPrefix + "xattr." + x.name, x.value})
	}

	slices.SortFunc(out, func(a, b reapiProperty) int {
		if a.name == b.name {
			return 0
		}

		if a.name < b.name {
			return -1
		}

		return 1
	})

	return out
}

// ordinaryMode is a mode `is_executable` already says.
func ordinaryMode(perm uint32) bool {
	return perm == 0o644 || perm == 0o755
}

// describeKind names a node type in the words a reader would use.
func describeKind(mode uint32) string {
	switch kindOf(mode) {
	case 'b':
		if fs.FileMode(mode)&fs.ModeCharDevice != 0 {
			return "a character device"
		}

		return "a block device"
	case 'p':
		return "a named pipe"
	case 's':
		return "a socket"
	default:
		return "not a file, a directory or a symlink"
	}
}

// join names a path for a refusal, with no leading separator at the root.
func join(at, name string) string {
	if at == "" {
		return name
	}

	return at + "/" + name
}
