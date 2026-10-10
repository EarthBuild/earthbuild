package layer_test

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// aLayerTar writes the shapes a real base image contains, so that a manifest
// built from the archive and one built from the unpacked tree have something to
// disagree about.
//
// Deliberately awkward: an implicit parent directory the archive never names, a
// hardlink, a symlink, an empty file, a whiteout marker, and times with
// nanoseconds - each one a place where reading the archive and reading the disk
// could plausibly differ.
func aLayerTar(t *testing.T) []byte {
	t.Helper()

	when := time.Unix(1700000000, 123456789)

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	write := func(h *tar.Header, body string) {
		t.Helper()

		h.ModTime = when
		h.Size = int64(len(body))

		err := tw.WriteHeader(h)
		if err != nil {
			t.Fatal(err)
		}

		if body != "" {
			_, err = tw.Write([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	write(&tar.Header{Typeflag: tar.TypeDir, Name: "usr/", Mode: 0o755}, "")
	write(&tar.Header{Typeflag: tar.TypeReg, Name: "usr/bin/tool", Mode: 0o755}, "the tool")
	write(&tar.Header{Typeflag: tar.TypeReg, Name: "usr/empty", Mode: 0o644}, "")
	write(&tar.Header{Typeflag: tar.TypeLink, Name: "usr/bin/same", Linkname: "usr/bin/tool", Mode: 0o755}, "")
	write(&tar.Header{Typeflag: tar.TypeSymlink, Name: "usr/link", Linkname: "bin/tool", Mode: 0o777}, "")
	write(&tar.Header{Typeflag: tar.TypeReg, Name: "etc/conf", Mode: 0o600}, "key=value")
	write(&tar.Header{Typeflag: tar.TypeReg, Name: "etc/.wh.gone", Mode: 0o644}, "")

	err := tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// TestAManifestReadFromTheArchiveIsTheManifestOfTheUnpackedTree.
//
// **This is what makes a lazy pull possible at all.** `ManifestID` is equal to
// `Take(root).ID` for the tree the manifest came from, so a layer can be named,
// authenticated and served from its manifest alone - without the tree ever being
// written. E654 measured that writing is 65% of a bare unpack and roughly 78% of
// the engine's, over 15034 files a build mostly never opens.
//
// The whole risk is a second definition of what a layer is. So the archive path
// is pinned against the walk path byte for byte, over shapes chosen to disagree:
// an implicit parent directory the archive never names, a hardlink, a symlink,
// an empty file and a whiteout.
func TestAManifestReadFromTheArchiveIsTheManifestOfTheUnpackedTree(t *testing.T) {
	t.Parallel()

	blob := aLayerTar(t)
	root := t.TempDir()

	got, err := image.UnpackApart(bytes.NewReader(blob), root)
	if err != nil {
		t.Fatal(err)
	}

	walked, err := layer.ManifestOwned(root, layer.IDMap{}, layer.IDMap{}, declarationOf(got))
	if err != nil {
		t.Fatal(err)
	}

	read, err := layer.ManifestFromTar(bytes.NewReader(blob))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(read, walked) {
		t.Fatalf("the archive says %d bytes of manifest and the tree says %d\n"+
			"  ids %v and %v\n"+
			"  a layer named one way and served the other is a layer the store\n"+
			"  cannot find and a peer cannot authenticate (I3)",
			len(read), len(walked), layer.ManifestID(read), layer.ManifestID(walked))
	}
}

// TestALayerCanBeNamedWithoutBeingWritten is the property stated directly, so a
// reader of the test list can see what the archive path is *for*.
func TestALayerCanBeNamedWithoutBeingWritten(t *testing.T) {
	t.Parallel()

	blob := aLayerTar(t)
	root := t.TempDir()

	got, err := image.UnpackApart(bytes.NewReader(blob), root)
	if err != nil {
		t.Fatal(err)
	}

	written, err := layer.TakeOwnedIn(root, layer.IDMap{}, layer.IDMap{}, declarationOf(got))
	if err != nil {
		t.Fatal(err)
	}

	read, err := layer.ManifestFromTar(bytes.NewReader(blob))
	if err != nil {
		t.Fatal(err)
	}

	if layer.ManifestID(read) != written.ID {
		t.Fatalf("the archive names the layer %v and the tree names it %v",
			layer.ManifestID(read), written.ID)
	}
}

// declarationOf is the archive's account of ownership, in the form the capture
// takes it. The tree cannot supply it: an unprivileged unpack could not grant it
// (E656).
func declarationOf(u image.Unpacked) map[string]layer.Owner {
	out := make(map[string]layer.Owner, len(u.Owners))
	for at, o := range u.Owners {
		out[at] = layer.Owner{UID: o.UID, GID: o.GID}
	}

	return out
}

// TestAPackReadFromTheArchiveIsThePackOfTheUnpackedTree.
//
// The other half of serving a layer nobody unpacked. `ManifestFromTar` lets a
// layer be *named*; this lets the part of it somebody asked for be *sent* - and
// between them a pulled blob can answer a `Fragmenter` without the tree ever
// existing.
//
// Byte-for-byte, for the reason `Pack`'s own comment gives: two encodings of one
// tree is the determinism problem E262 exists to avoid, and a fragment that
// packed differently would capture to a different identity and be filed as a
// layer nobody asked for.
func TestAPackReadFromTheArchiveIsThePackOfTheUnpackedTree(t *testing.T) {
	t.Parallel()

	blob := aLayerTar(t)
	root := t.TempDir()

	got, err := image.UnpackApart(bytes.NewReader(blob), root)
	if err != nil {
		t.Fatal(err)
	}

	own := declarationOf(got)

	for _, want := range [][]string{
		nil,
		{"etc/conf"},
		{"usr/bin/tool"},
		{"usr"},
		{"etc/conf", "usr/link"},
		// A path the layer does not have: a prediction that named something
		// absent is a step that looked and did not find (I5), not an error.
		{"etc/conf", "no/such/file"},
	} {
		var fromTree, fromTar bytes.Buffer

		err = layer.PackOwned(root, &fromTree, want, own)
		if err != nil {
			t.Fatalf("%v: %v", want, err)
		}

		err = layer.PackPathsFromTar(bytes.NewReader(blob), &fromTar, want)
		if err != nil {
			t.Fatalf("%v: %v", want, err)
		}

		if !bytes.Equal(fromTree.Bytes(), fromTar.Bytes()) {
			t.Errorf("want %v: the tree packed %d bytes and the archive %d",
				want, fromTree.Len(), fromTar.Len())
		}
	}
}

// TestOnePassGivesBothTheProofAndTheFragment.
//
// **An archive is read forwards once, and both answers are in it.** Asking for
// the proof and then the fragment costs two decompressions - measured at 1.321s
// and 1.287s of a 2.612s lazy materialisation of `golang:1.26-alpine`'s dominant
// layer, so the second pass is half the cost of the whole thing.
//
// The two must be exactly what the separate calls produce, or a caller that
// holds a manifest from one path and a fragment from the other cannot check the
// second against the first.
func TestOnePassGivesBothTheProofAndTheFragment(t *testing.T) {
	t.Parallel()

	blob := aLayerTar(t)

	for _, want := range [][]string{nil, {"etc/conf"}, {"usr"}, {"etc/conf", "usr/link"}} {
		var separate bytes.Buffer

		err := layer.PackPathsFromTar(bytes.NewReader(blob), &separate, want)
		if err != nil {
			t.Fatalf("%v: %v", want, err)
		}

		alone, err := layer.ManifestFromTar(bytes.NewReader(blob))
		if err != nil {
			t.Fatalf("%v: %v", want, err)
		}

		var together bytes.Buffer

		both, err := layer.FragmentFromTar(bytes.NewReader(blob), &together, want)
		if err != nil {
			t.Fatalf("%v: %v", want, err)
		}

		if !bytes.Equal(both, alone) {
			t.Errorf("want %v: one pass and two disagree about the proof", want)
		}

		if !bytes.Equal(together.Bytes(), separate.Bytes()) {
			t.Errorf("want %v: one pass and two disagree about the fragment", want)
		}
	}
}

// TestTheArchiveReaderDropsTheSameAttributesTheWalkDoes.
//
// **A second implementation is only safe while it agrees**, and this one did
// not. `readXattrs` excludes three families: `user.overlay.` and
// `trusted.overlay.`, which record which lower inode a file was copied up from
// and are a property of an assembly rather than of a file (E132), and
// `com.apple.`, which macOS stamps on files of its own accord.
//
// The archive reader took every `SCHILY.xattr.*` PAX record as written. An
// image built from an overlay upper layer carries exactly those attributes, so
// its manifest read from the archive would not match its manifest read from the
// tree - and `fleet.Blobs` refuses to serve a blob whose manifest hashes
// elsewhere, which means it would decline to serve a layer it holds and is
// right about.
func TestTheArchiveReaderDropsTheSameAttributesTheWalkDoes(t *testing.T) {
	t.Parallel()

	blob := aLayerTarWithXattrs(t, map[string]string{
		"SCHILY.xattr.trusted.overlay.redirect": "/some/lower/path",
		"SCHILY.xattr.com.apple.provenance":     "\x01\x02\x00\xef",
		"SCHILY.xattr.user.overlay.impure":      "y",
		"SCHILY.xattr.user.kept":                "this one is the layer's",
	})

	root := t.TempDir()

	got, err := image.UnpackApart(bytes.NewReader(blob), root)
	if err != nil {
		t.Fatal(err)
	}

	walked, err := layer.ManifestOwned(root, layer.IDMap{}, layer.IDMap{}, declarationOf(got))
	if err != nil {
		t.Fatal(err)
	}

	read, err := layer.ManifestFromTar(bytes.NewReader(blob))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(read, walked) {
		t.Fatalf("the archive and the tree disagree about a layer's attributes:"+
			"\n  archive %v\n  tree    %v"+
			"\n  a blob whose manifest hashes elsewhere is refused, so this is a"+
			"\n  layer the store holds and declines to serve",
			layer.ManifestID(read), layer.ManifestID(walked))
	}
}

// aLayerTarWithXattrs is one file carrying the PAX records given.
func aLayerTarWithXattrs(t *testing.T, records map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "usr/bin/ping", Mode: 0o755,
		Size: 4, ModTime: time.Unix(1700000000, 0),
		PAXRecords: records, Format: tar.FormatPAX,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tw.Write([]byte("ping"))
	if err != nil {
		t.Fatal(err)
	}

	err = tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// TestASpecialFileReadsTheSameFromTheArchiveAsFromTheTree.
//
// A fifo, because it is the one special file an unprivileged process may
// create: `mknod` for a character or block device needs root, and this test has
// to run where the engine runs. The rule it pins is the general one - what the
// archive reader records for a special entry is what a walk of the created node
// reports.
func TestASpecialFileReadsTheSameFromTheArchiveAsFromTheTree(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeFifo, Name: "run/pipe", Mode: 0o644,
		ModTime: time.Unix(1700000000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}

	err = tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()

	got, err := image.UnpackApart(bytes.NewReader(buf.Bytes()), root)
	if err != nil {
		t.Fatal(err)
	}

	_, statErr := os.Lstat(filepath.Join(root, "run", "pipe"))
	if statErr != nil {
		t.Skipf("this platform did not create the fifo: %v", statErr)
	}

	walked, err := layer.ManifestOwned(root, layer.IDMap{}, layer.IDMap{}, declarationOf(got))
	if err != nil {
		t.Fatal(err)
	}

	read, err := layer.ManifestFromTar(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(read, walked) {
		t.Fatalf("the archive and the tree disagree about a fifo:\n  archive %v\n  tree    %v",
			layer.ManifestID(read), layer.ManifestID(walked))
	}
}

// TestLinkedNamesShareWhatTheInodeEndsUpWith.
//
// **A hardlink is one inode with several names, so its metadata is whatever was
// applied last.** The unpacker writes the target, stamps it, links the second
// name to it and stamps *that* - and a chtimes on either name moves the inode,
// so both names then report the second header's time.
//
// The archive reader copied the target's entry to every name in the group, which
// is the first header's time. An archive whose link header declares a different
// time than its target - and nothing stops one - therefore read one way from the
// blob and another from the tree.
func TestLinkedNamesShareWhatTheInodeEndsUpWith(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "usr/bin/tool", Mode: 0o755,
		Size: 8, ModTime: time.Unix(1700000000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tw.Write([]byte("the tool"))
	if err != nil {
		t.Fatal(err)
	}

	// The same inode under a second name, declared with a *later* time. The
	// unpacker applies it to the shared inode, so it is the time both names have.
	err = tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeLink, Name: "usr/bin/same", Linkname: "usr/bin/tool",
		Mode: 0o755, ModTime: time.Unix(1700009999, 0),
	})
	if err != nil {
		t.Fatal(err)
	}

	err = tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()

	got, err := image.UnpackApart(bytes.NewReader(buf.Bytes()), root)
	if err != nil {
		t.Fatal(err)
	}

	walked, err := layer.ManifestOwned(root, layer.IDMap{}, layer.IDMap{}, declarationOf(got))
	if err != nil {
		t.Fatal(err)
	}

	read, err := layer.ManifestFromTar(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(read, walked) {
		t.Fatalf("the archive and the tree disagree about a hardlinked pair:"+
			"\n  archive %v\n  tree    %v"+
			"\n  the inode has one set of metadata and the archive has two headers",
			layer.ManifestID(read), layer.ManifestID(walked))
	}
}

// TestAnArchiveThatCannotBeUnpackedIsNotDescribed.
//
// **The reader claims to describe the tree an archive unpacks to, so for an
// archive that unpacks to nothing it must claim nothing.** `safePath` refuses an
// entry with an empty name, an absolute one, or one that climbs out with `..`,
// and refusing it fails the whole unpack - there is no such tree. The reader
// described one happily.
//
// Not a hole: `layer.Unpack` joins a fragment's paths with `safeJoin`, so a
// hostile stream cannot write outside its root however it was described. What it
// is, is a false claim - a manifest for a layer that could never exist, offered
// by a source that says it holds one.
//
// The lexical half of the rule only. `safePath` also refuses a parent that
// resolves through a symlink out of the layer, which needs a filesystem to
// resolve against and a reader that builds no tree has none. That case reaches
// the same place by a different route: no such layer was ever unpacked, so no id
// matches, so `fleet.Blobs` never serves it.
func TestAnArchiveThatCannotBeUnpackedIsNotDescribed(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"../escape", "/etc/passwd", "usr/../../out", ""} {
		var buf bytes.Buffer

		tw := tar.NewWriter(&buf)

		err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: name, Mode: 0o644,
			Size: 2, ModTime: time.Unix(1700000000, 0),
		})
		if err != nil {
			// A name this writer will not even emit is refused earlier than
			// this test reaches, which is the same answer.
			continue
		}

		_, err = tw.Write([]byte("hi"))
		if err != nil {
			t.Fatal(err)
		}

		err = tw.Close()
		if err != nil {
			t.Fatal(err)
		}

		// The unpacker's answer, which is the one the reader has to agree with.
		_, unpackErr := image.UnpackApart(bytes.NewReader(buf.Bytes()), t.TempDir())
		if unpackErr == nil {
			t.Fatalf("%q was unpacked, so this test no longer describes the rule", name)
		}

		_, readErr := layer.ManifestFromTar(bytes.NewReader(buf.Bytes()))
		if readErr == nil {
			t.Errorf("%q cannot be unpacked and was described anyway:"+
				"\n  a manifest for a layer that could never exist", name)
		}
	}
}

// TestAnArchiveThatNamesAPathTwiceIsNotDescribed.
//
// E668's shape again, from the other rule the unpacker enforces: **"a layer
// naming a path twice is an archive that cannot be trusted to mean anything,
// and choosing the last of them would be a guess about which entry was
// intended"**. The unpacker refuses it and the whole unpack fails, so there is
// no tree - and the reader took the later entry and described one.
//
// Directories are the exception the unpacker already makes for itself: two
// layers both containing `/usr/bin` are not in conflict, and one layer naming it
// twice is the same statement made twice.
func TestAnArchiveThatNamesAPathTwiceIsNotDescribed(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	for _, body := range []string{"first", "second"} {
		err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: "usr/conf", Mode: 0o644,
			Size: int64(len(body)), ModTime: time.Unix(1700000000, 0),
		})
		if err != nil {
			t.Fatal(err)
		}

		_, err = tw.Write([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
	}

	err := tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, unpackErr := image.UnpackApart(bytes.NewReader(buf.Bytes()), t.TempDir())
	if unpackErr == nil {
		t.Fatal("the unpacker accepted a path named twice, so this test no" +
			" longer describes the rule")
	}

	_, readErr := layer.ManifestFromTar(bytes.NewReader(buf.Bytes()))
	if readErr == nil {
		t.Error("an archive naming a path twice was described anyway:" +
			"\n  the unpacker refuses to guess which entry was meant, and a" +
			"\n  manifest that guessed would name a layer nobody can produce")
	}
}

// TestADirectoryNamedTwiceIsFine is the exception, and it is not a nicety: a
// `tar -C rootfs .` commonly emits a directory header before its contents and
// again for a later subtree.
func TestADirectoryNamedTwiceIsFine(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	for range 2 {
		err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeDir, Name: "usr/", Mode: 0o755,
			ModTime: time.Unix(1700000000, 0),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	err := tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()

	got, unpackErr := image.UnpackApart(bytes.NewReader(buf.Bytes()), root)
	if unpackErr != nil {
		t.Skipf("the unpacker refuses a directory named twice: %v", unpackErr)
	}

	walked, err := layer.ManifestOwned(root, layer.IDMap{}, layer.IDMap{}, declarationOf(got))
	if err != nil {
		t.Fatal(err)
	}

	read, err := layer.ManifestFromTar(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("a directory named twice was refused: %v", err)
	}

	if !bytes.Equal(read, walked) {
		t.Errorf("the archive and the tree disagree about a directory named twice:"+
			"\n  archive %v\n  tree    %v",
			layer.ManifestID(read), layer.ManifestID(walked))
	}
}
