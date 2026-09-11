package layer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Manifest is the byte stream a layer's identity is the hash of.
//
// **A layer is hashed over metadata and per-file content digests, never file
// bytes** (§3.3). So the whole of what the digest covers is small - about a
// hundred bytes an entry, two megabytes for a base of twenty thousand files -
// and it can simply be sent.
//
// That is what makes a fragment verifiable. E282 recorded that a subset carried
// no inclusion proof and concluded the digest would have to become a tree,
// changing §3.2 and every digest this engine has computed. It does not: hash the
// manifest, compare it to the layer's name, and every path's content digest is
// authenticated - after which a fragment is checked file by file against digests
// nobody could forge without breaking the layer's name (E284).
//
// An O(n) proof, where n is entries rather than bytes. That is the trade, and at
// two megabytes against hundreds it is not close.
func Manifest(root string) ([]byte, error) {
	return ManifestIn(root, IDMap{}, IDMap{})
}

// ManifestIn is Manifest with ownership translated as TakeIn translates it.
//
// The same argument as TakeIn's: a manifest that did not agree with the digest
// about ownership would authenticate nothing, because it would hash to a
// different layer.
func ManifestIn(root string, uids, gids IDMap) ([]byte, error) {
	return ManifestOwned(root, uids, gids, nil)
}

// ManifestOwned is ManifestIn with ownership taken from a declaration.
//
// The third of the three walks that hash ownership, and the one whose absence
// fails safe rather than loudly: a manifest that disagrees with the layer it
// claims to describe authenticates nothing, so a fragment checked against it is
// refused. A lazy base between two machines with different users would simply
// never work (E313).
func ManifestOwned(
	root string, uids, gids IDMap, own map[string]Owner,
) ([]byte, error) {
	entries, _, err := walk(root)
	if err != nil {
		return nil, fmt.Errorf("read the layer at %s: %w", root, err)
	}

	entries = declared(entries, own)

	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	return encodeEntries(entries, uids, gids), nil
}

// encodeEntries writes the manifest for entries already walked.
//
// Split out so a capture can hand back the manifest for what it captured
// without walking the tree a second time: the two produce the same bytes
// because they are the same function over the same entries, which is the
// property `TestACaptureCanHandBackItsManifest` pins.
//
// The caller sorts. `ManifestOwned` and `capture` both do, for the same reason -
// directory order is the filesystem's and must not reach a digest.
func encodeEntries(entries []entry, uids, gids IDMap) []byte {
	var buf bytes.Buffer

	e := ir.NewEncoder(&buf)
	e.Count(len(entries))

	for _, en := range entries {
		en.uid = uids.Outside(en.uid)
		en.gid = gids.Outside(en.gid)

		en.hash(e, withTimes)
	}

	return buf.Bytes()
}

// ManifestID is the layer identity a manifest attests to.
//
// Equal to `Take(root).ID` for the tree the manifest came from, which is the
// whole property: a peer cannot send a manifest that authenticates paths the
// layer does not have without it hashing to a different layer.
func ManifestID(m []byte) ir.NodeID {
	h := ir.NewHasher()
	h.Fixed(m)

	return h.Sum()
}

// VerifyFragment checks a fragment against a manifest, file by file.
//
// **The manifest is the proof.** Its hash is the layer's name (see Manifest), so
// every content digest in it is as trustworthy as the name - and a file whose
// digest does not match is not part of that layer, however plausible it looks.
//
// A path the manifest does not mention is refused too. A fragment carrying
// something extra is not a generous fragment: it is a peer adding a file to
// somebody's base, which is the whole of what an attacker would want here.
//
// What this does *not* check is that the fragment is complete. It cannot: a
// fragment is a subset by construction, and which subset was asked for is the
// caller's business. Absence is the caller's problem and presence is this
// function's.
func VerifyFragment(manifest []byte, root string) error {
	want, err := readManifest(manifest)
	if err != nil {
		return err
	}

	got, _, err := walk(root)
	if err != nil {
		return fmt.Errorf("read the fragment at %s: %w", root, err)
	}

	for _, en := range got {
		sealed, ok := want[en.path]
		if !ok {
			return fmt.Errorf("%w: %s is not in this layer", ErrMalformed, en.path)
		}

		if got := fragmentSeal(en); got != sealed {
			return fmt.Errorf("%w: %s is not what this layer says it is"+
				" (sealed %v, found %v)", ErrMalformed, en.path, sealed, got)
		}
	}

	return nil
}

// fragmentSeal is what a fragment's entry is checked against.
//
// **Every field of §3.3 the receiver can reproduce** (I13, green paper C.4.1). Verification used to
// compare the content digest and nothing else, discarding the mode, kind, size,
// device, link and extended attributes the manifest was already carrying - so a
// peer could send the right bytes with the wrong mode and a step would read
// something the layer does not describe (E324). Since E323 the lazy path is the
// one that wins, which makes this the check between a fleet and a wrong build
// rather than a corner (§5.3, I2).
//
// Two fields are outside it, each by argument:
//
//   - **ownership**, because restoring it needs privilege a worker does not have
//   - the same fact that made a whole layer capture under the wrong digest
//     (E313). A fragment is judged by the manifest's own declaration, so a peer
//     cannot lie about it usefully; it is simply not what the disk is compared
//     against;
//   - **hardlinks**, because a fragment is a subset and a link's partner may not
//     be in it. Sealing that field would refuse honest fragments of any layer
//     built by a package manager.
//
// Zeroed rather than skipped, on both sides, so the two cannot drift apart.
func fragmentSeal(e entry) ir.NodeID {
	e.uid, e.gid, e.hardlink = 0, 0, ""

	h := ir.NewHasher()
	e.hash(&h.Encoder, withTimes)

	return h.Sum()
}

// readManifest is every path a manifest names and the seal a fragment of it is
// checked against. See fragmentSeal.
func readManifest(m []byte) (map[string]ir.NodeID, error) {
	entries, err := decodeManifest(m)
	if err != nil {
		return nil, err
	}

	out := make(map[string]ir.NodeID, len(entries))
	for _, e := range entries {
		out[e.path] = fragmentSeal(e)
	}

	return out, nil
}

// File is what a manifest records about one regular file's contents.
//
// Not the whole entry: a caller comparing two files across layers has the
// digest and the size, and every other field is about where the file sits
// rather than what it holds.
type File struct {
	// Content is the digest of the bytes, as green paper §3.3 defines it.
	Content ir.NodeID
	// Size is what the layer says the file is, which is how a reader tells the
	// manifest from a file that has changed under it.
	Size int64
}

// Files is what a manifest says every regular file in its layer holds.
//
// **The point of keeping the manifest.** Every digest here was computed by the
// walk that made the layer; a reader with the manifest can tell two files apart,
// or the same, without opening either - which is what lets `COPY --sync` decide
// a tree is unchanged without reading it twice over.
//
// Regular files only. A directory, a link and a whiteout have no contents, and
// handing back a zero digest for them would let a caller conclude two of them
// were identical because neither had anything to compare.
func Files(m []byte) (map[string]File, error) {
	entries, err := decodeManifest(m)
	if err != nil {
		return nil, err
	}

	out := make(map[string]File, len(entries))

	for _, e := range entries {
		if kindOf(e.mode) != 'f' {
			continue
		}

		out[e.path] = File{Content: e.content, Size: e.size}
	}

	return out, nil
}

// decodeManifest reads back the entries Manifest wrote.
//
// The field order mirrors `entry.hash` because it is the same encoding read the
// other way. That coupling is the point - a manifest is not a second format, it
// is the bytes the digest is already over - and it is why the round trip is
// asserted rather than assumed.
func decodeManifest(m []byte) ([]entry, error) {
	d := &reader{r: bytes.NewReader(m)}

	n := d.count(maxEntries)
	if d.err != nil {
		return nil, d.err
	}

	out := make([]entry, 0, n)

	for range n {
		var e entry

		e.path = d.str()

		kind := d.fixed(1)

		fixed := d.fixed(40)
		if d.err == nil {
			e.mode = binary.BigEndian.Uint32(fixed[0:])
			e.uid = binary.BigEndian.Uint32(fixed[4:])
			e.gid = binary.BigEndian.Uint32(fixed[8:])
			e.mtimeSec = int64(binary.BigEndian.Uint64(fixed[12:])) //nolint:gosec // two's complement round-trips
			e.mtimeNs = binary.BigEndian.Uint32(fixed[20:])
			e.size = int64(binary.BigEndian.Uint64(fixed[24:])) //nolint:gosec // never negative
			e.rdev = binary.BigEndian.Uint64(fixed[32:])
		}

		if c := d.fixed(len(e.content)); d.err == nil {
			copy(e.content[:], c)
		}

		e.link = d.str()
		e.hardlink = d.str()

		x := d.count(1 << 16)
		for range x {
			e.xattrs = append(e.xattrs, xattr{name: d.str(), value: d.str()})
		}

		if d.err != nil {
			return nil, d.err
		}

		// The kind travels beside the mode because platforms report the type
		// bits differently, and `fragmentSeal` re-derives it from the mode on
		// both sides - so the byte itself would go unused, and an unused field
		// on the wire is a field a peer can set to anything. Checked instead:
		// disagreeing with its own mode is malformed, whatever it claims to be.
		if len(kind) == 1 && kind[0] != kindOf(e.mode) {
			return nil, fmt.Errorf("%w: %s is a %q and its mode says %q",
				ErrMalformed, e.path, kind[0], kindOf(e.mode))
		}

		out = append(out, e)
	}

	return out, nil
}
