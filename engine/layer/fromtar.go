package layer

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// undeclaredDirMode and unpackEpoch are what `engine/image` gives a directory
// the archive never described (E655). Named here too because this path has to
// produce the same entry the walk would read off the disk, and a disagreement
// about either is a layer with two names.
const undeclaredDirMode = 0o755

var unpackEpoch = time.Unix(0, 0)

// paxXattr prefixes the PAX records that carry extended attributes.
const paxXattr = "SCHILY.xattr."

// ManifestFromTar builds a layer's manifest by reading the archive, without the
// layer ever being written.
//
// **This is what makes a lazy pull possible.** `ManifestID` equals
// `Take(root).ID` for the tree a manifest describes, so a layer read this way
// can be named, authenticated and served without the unpack - and E654 measured
// the unpack at roughly 78% filesystem work over 15034 files a build mostly
// never opens.
//
// Ownership is the archive's own, which is the account
// `engine/image` now hands to the store as a declaration - so the id is the
// same whether the unpack could grant it or not (E656).
//
// The reader is consumed. Content digests are taken from the bytes as they pass,
// with the same hasher `contentDigest` uses on a file.
//
// **What it cannot know is whatever the local filesystem decides.** Two cases,
// and they are the same case:
//
//   - a special file. `makeSpecial` attempts a `mknod` and tolerates EPERM, so a
//     character device is in the layer on a privileged Linux unpack and absent
//     on a developer's Mac.
//   - two paths differing only in case. `replacing` refuses them, but only after
//     asking the filesystem whether it can hold both - on a case-sensitive one
//     the image unpacks exactly as it was built.
//
// This reads the archive rather than the tree, so it describes the entries
// either way. Where the local machine would have refused, the manifest does not
// match, `fleet.Blobs` refuses to serve a blob whose manifest hashes elsewhere,
// and the layer is unpacked the ordinary way: slower and correct, which is this
// path's failure mode throughout.
//
// Stated because both alternatives are worse. A reader that probed the
// filesystem would write to a tree it was asked not to build; one that refused
// lexically would reject layers that unpack perfectly well on the machine
// asking.
func ManifestFromTar(r io.Reader) ([]byte, error) {
	entries, err := entriesFromTar(r)
	if err != nil {
		return nil, err
	}

	return encodeManifest(entries), nil
}

// encodeManifest is the half ManifestOwned and ManifestFromTar share.
//
// One function, for the reason `capture` is one: a second place that sorted and
// encoded entries would be a second definition of what a layer is, and the two
// would agree until they did not.
func encodeManifest(entries []entry) []byte {
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	var buf bytes.Buffer

	e := ir.NewEncoder(&buf)
	e.Count(len(entries))

	for _, en := range entries {
		en.hash(e, withTimes)
	}

	return buf.Bytes()
}

// entriesFromTar reads the archive into the entries a walk of the unpacked tree
// would produce.
//
// Every difference between "what the archive says" and "what the disk would
// say" is handled here, and each one is a place the two could silently diverge:
// directories the archive never names, hardlinks identified by walk order
// rather than by which entry came first, and ownership the unpack could not
// grant.
func entriesFromTar(r io.Reader) ([]entry, error) {
	return entriesFromTarKeeping(r, nil, nil)
}

// entriesFromTarKeeping is entriesFromTar with the bodies of the entries a
// fragment wants retained as they go past.
//
// **One pass, and only the wanted bytes held.** A pack read from an archive
// cannot go back for a file, and holding the whole layer to filter it afterwards
// would give up the thing reading from an archive is for. `keep` decides as each
// entry arrives; nil keeps no bodies at all, which is what a manifest needs.
func entriesFromTarKeeping(r io.Reader, keep *keeper, bodies map[ir.NodeID][]byte) ([]entry, error) {
	var (
		byPath = map[string]*entry{}
		// links maps a target path to every path hardlinked to it, which is not
		// the same question the archive answers: the archive says "this links
		// to that", and the disk says "these share an inode".
		links = map[string][]string{}
		// stamps is each link header in archive order, because the inode keeps
		// what was applied last rather than what its target declared.
		stamps []entry
	)

	tr := tar.NewReader(r)

	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("read the layer archive: %w", err)
		}

		name, err := containedName(h.Name)
		if err != nil {
			return nil, err
		}

		if name == "" {
			continue // the layer's own root, which every `tar -C rootfs .` names
		}

		if h.Typeflag == tar.TypeLink {
			target, targetErr := containedName(h.Linkname)
			if targetErr != nil {
				return nil, fmt.Errorf("hardlink %q: %w", h.Name, targetErr)
			}

			links[target] = append(links[target], name)

			// **Kept, because a link's header stamps the shared inode.** The
			// unpacker links the name and then calls setMeta on it, and a
			// chtimes on any name of an inode moves the inode - so the last
			// header applied is the metadata every name then reports.
			stamp, stampErr := entryFromHeader(h, name, tr, nil)
			if stampErr != nil {
				return nil, stampErr
			}

			stamps = append(stamps, stamp)

			continue
		}

		// Collected only when somebody asked for this path: the archive can
		// only be read forwards, so a body not held now is a body gone.
		var into *bytes.Buffer
		if bodies != nil && keep != nil && keep.keeps(name) && h.FileInfo().Mode().IsRegular() {
			into = &bytes.Buffer{}
		}

		e, err := entryFromHeader(h, name, tr, into)
		if err != nil {
			return nil, err
		}

		// **Keyed by digest, not by path.** A hardlinked file's bytes arrive
		// under whichever name the archive listed first, and the walk calls the
		// lexicographically first name the original - so the name carrying the
		// body and the name the pack asks about are routinely different.
		if into != nil {
			bodies[e.content] = into.Bytes()
		}

		// **Refused, as the unpacker refuses it**: "a layer naming a path twice
		// is an archive that cannot be trusted to mean anything, and choosing
		// the last of them would be a guess about which entry was intended". So
		// there is no tree, and describing one would name a layer nobody can
		// produce.
		//
		// Directories excepted, for the unpacker's own reason: two layers both
		// containing `/usr/bin` are not in conflict, and one layer saying it
		// twice is the same statement made twice.
		if _, again := byPath[name]; again && !e.isDir() {
			return nil, fmt.Errorf("%w: the layer names %q twice", ErrMalformed, h.Name)
		}

		byPath[name] = &e
	}

	addImplicitDirs(byPath)
	applyHardlinks(byPath, links, stamps)

	out := make([]entry, 0, len(byPath))
	for _, e := range byPath {
		out = append(out, *e)
	}

	return out, nil
}

// entryFromHeader is one archive entry as the disk would report it.
func entryFromHeader(h *tar.Header, name string, tr io.Reader, into *bytes.Buffer) (entry, error) {
	info := h.FileInfo()

	e := entry{
		path:     name,
		mode:     hashedMode(uint32(info.Mode())),
		mtimeSec: h.ModTime.Unix(),
		mtimeNs:  uint32(h.ModTime.Nanosecond()), //nolint:gosec // < 1e9
		uid:      uint32(h.Uid),                  //nolint:gosec // an archive's uid
		gid:      uint32(h.Gid),                  //nolint:gosec // an archive's gid
	}

	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		e.link = h.Linkname

	case info.Mode().IsRegular():
		e.size = h.Size

		sum := ir.NewHasher()

		// Into the hasher and, when somebody wants the bytes, into a buffer at
		// the same time.
		var sink io.Writer = sum
		if into != nil {
			sink = io.MultiWriter(sum, into)
		}

		_, err := io.Copy(sink, tr)
		if err != nil {
			return entry{}, fmt.Errorf("read %q from the archive: %w", h.Name, err)
		}

		e.content = sum.Sum()

	case info.Mode()&(fs.ModeDevice|fs.ModeCharDevice) != 0:
		//nolint:gosec // device numbers from the archive
		e.rdev = mkdev(uint32(h.Devmajor), uint32(h.Devminor))
	}

	e.xattrs = xattrsOf(h)

	return e, nil
}

// xattrsOf reads the PAX records that carry extended attributes, sorted by name
// so the archive's ordering cannot reach the digest - which is the rule
// `readXattrs` follows for the same reason.
func xattrsOf(h *tar.Header) []xattr {
	var xs []xattr

	for k, v := range h.PAXRecords {
		short, ok := strings.CutPrefix(k, paxXattr)
		if !ok {
			continue
		}

		// **The same exclusions the walk applies**, from the one function that
		// states them. `readXattrs` drops attributes that record how a tree was
		// assembled rather than what it contains, and an archive reader that
		// kept them would produce a manifest the tree can never match - so a
		// blob holding such a layer would be refused as attesting to a
		// different one.
		if assembledBy(short) {
			continue
		}

		xs = append(xs, xattr{name: short, value: v})
	}

	sort.Slice(xs, func(i, j int) bool { return xs[i].name < xs[j].name })

	return xs
}

// addImplicitDirs invents the directories the archive relies on and never names.
//
// The unpacker creates them with `os.MkdirAll`, which is why they carry a stated
// mode, the epoch and root's ownership rather than anything of their own (E655,
// E656). Nothing describes them, so everything about them has to be said here
// rather than inherited from whoever happened to run the unpack.
func addImplicitDirs(byPath map[string]*entry) {
	for name := range byPath {
		for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
			if _, ok := byPath[dir]; ok {
				break
			}

			byPath[dir] = &entry{
				path:     dir,
				mode:     uint32(fs.ModeDir | undeclaredDirMode),
				mtimeSec: unpackEpoch.Unix(),
				mtimeNs:  uint32(unpackEpoch.Nanosecond()), //nolint:gosec // < 1e9
				uid:      0,
				gid:      0,
			}
		}
	}
}

// applyHardlinks makes linked paths what the disk says they are: one inode with
// several names.
//
// **The archive's answer is not the disk's.** A tar says "b links to a"; a walk
// finds two paths sharing an inode and calls the *first one it reaches* the
// original, which is lexicographic order rather than archive order. Every member
// also shares the inode's metadata, so the group agrees about mode, times and
// ownership however the archive described each name.
func applyHardlinks(byPath map[string]*entry, links map[string][]string, stamps []entry) {
	// Which group each linked name belongs to, indexed once. Scanning the
	// groups per header would be quadratic, and busybox images name several
	// hundred links to one inode.
	group := map[string]string{}

	for target, names := range links {
		for _, name := range names {
			group[name] = target
		}
	}

	// **The last header applied to a group is the group's metadata.** Walked in
	// archive order so the last one wins, exactly as the unpacker's successive
	// `setMeta` calls leave it on the shared inode.
	last := map[string]entry{}

	for _, e := range stamps {
		if target, ok := group[e.path]; ok {
			last[target] = e
		}
	}

	for target, names := range links {
		src, ok := byPath[target]
		if !ok {
			// A link to something this layer does not have. The unpacker fails
			// on it, so an archive reaching here is one nothing will unpack -
			// leave the link out rather than invent a file for it.
			continue
		}

		members := append([]string{target}, names...)
		sort.Strings(members)

		// Content and size come from the target, which is the only entry that
		// carried bytes; times, mode and ownership from whichever header the
		// unpacker applied last.
		shared := *src

		if stamped, ok := last[target]; ok {
			shared.mode = stamped.mode
			shared.mtimeSec, shared.mtimeNs = stamped.mtimeSec, stamped.mtimeNs
			shared.uid, shared.gid = stamped.uid, stamped.gid
			shared.xattrs = stamped.xattrs
		}

		for _, name := range members {
			e := shared
			e.path = name

			if name != members[0] {
				e.hardlink = members[0]
			}

			byPath[name] = &e
		}
	}
}

// PackPathsFromTar writes the part of a layer somebody asked for, reading the
// archive rather than an unpacked tree.
//
// **The other half of serving a layer nobody unpacked.** `ManifestFromTar`
// lets a pulled blob *name* a layer; this lets it *send* the part of one, so a
// `Fragmenter` can answer from 61MB of compressed bytes rather than from 228MB
// of files - E654 measured writing those files at roughly 78% of the unpack,
// over 15034 entries a build mostly never opens.
//
// Byte-for-byte what `PackOwned` produces for the tree that archive unpacks to,
// which `TestAPackReadFromTheArchiveIsThePackOfTheUnpackedTree` pins. It has to
// be: two encodings of one tree is the determinism problem E262 exists to
// avoid, and a fragment encoded differently captures to an identity nobody
// asked for.
//
// Only the wanted bodies are held. The archive can be read only forwards, so
// the decision is made as each entry passes and the memory is the fragment's
// rather than the layer's.
func PackPathsFromTar(r io.Reader, w io.Writer, want []string) error {
	_, err := fragmentFromTar(r, w, want, false)

	return err
}

// FragmentFromTar reads an archive once and produces both the layer's proof and
// the part of it somebody asked for.
//
// **Both answers are in one pass, and asking twice costs a second
// decompression** - 1.287s of a 2.612s lazy materialisation of
// `golang:1.26-alpine`'s dominant layer, which is half of it. A gzip member
// cannot be entered in the middle, so a second answer means a second pass over
// the whole thing.
//
// Byte-for-byte what `ManifestFromTar` and `PackPathsFromTar` produce
// separately, which has to hold: a caller checking a fragment from here against
// a manifest from there is the ordinary case, and `VerifyFragment` compares
// digests rather than intentions.
func FragmentFromTar(r io.Reader, w io.Writer, want []string) ([]byte, error) {
	return fragmentFromTar(r, w, want, true)
}

func fragmentFromTar(r io.Reader, w io.Writer, want []string, proof bool) ([]byte, error) {
	keep := newKeeper(want)
	bodies := map[ir.NodeID][]byte{}

	entries, err := entriesFromTarKeeping(r, keep, bodies)
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	// **The manifest is of the whole layer, the pack of the part.** A manifest
	// covering only the fragment would hash to something that is not the
	// layer's name, and the name is the whole of what makes a fragment
	// checkable.
	var manifest []byte
	if proof {
		manifest = encodeManifest(entries)
	}

	entries = keeping(entries, want)

	err = encodePack(w, entries, func(en entry) ([]byte, error) {
		body, ok := bodies[en.content]
		if !ok {
			// A file that survived the filter and whose bytes were not kept is
			// a disagreement between two readings of one rule, not a missing
			// file - so it is an error rather than an empty body, which would
			// be a fragment quietly claiming the file is empty.
			return nil, fmt.Errorf("%w: %s was packed without its contents"+
				"\n  the filter that chose entries and the one that kept bodies"+
				" disagreed, and a fragment cannot say so afterwards",
				ErrMalformed, en.path)
		}

		return body, nil
	}, "")
	if err != nil {
		return nil, err
	}

	return manifest, nil
}

// containedName is an archive entry's path inside the layer, or an error saying
// it is not one.
//
// **The lexical half of `safePath`.** The unpacker refuses an empty name, an
// absolute one, or one that climbs out with `..`, and refusing it fails the
// whole unpack - so an archive containing one describes no tree at all, and a
// reader that described one anyway would offer a manifest for a layer that could
// never exist.
//
// The other half of `safePath` - a parent that resolves through a symlink out of
// the layer - needs a filesystem to resolve against, and a reader that builds no
// tree has none. That case reaches the same place by a different route: no such
// layer was ever unpacked, so no id matches it, so `fleet.Blobs` refuses to
// serve. Slower and correct, which is this whole path's failure mode.
//
// An empty result means the layer's own root, which every `tar -C rootfs .`
// names first and which is a member of nothing.
func containedName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: layer entry has an empty name", ErrMalformed)
	}

	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("%w: layer entry %q names an absolute path", ErrMalformed, name)
	}

	clean := path.Clean(strings.TrimPrefix(name, "./"))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: layer entry %q escapes the layer", ErrMalformed, name)
	}

	if clean == "." || clean == "/" {
		return "", nil
	}

	return clean, nil
}

// isDir reports whether an entry is a directory, by the same reading of the mode
// that `kindOf` uses.
func (e entry) isDir() bool { return fs.FileMode(e.mode).IsDir() }
