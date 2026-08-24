package layer_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// tree builds a small but awkward layer: nesting, an empty directory, a
// symlink, two files with identical contents, and modes that are not the
// default.
func tree(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	must := func(err error) {
		t.Helper()

		if err != nil {
			t.Fatal(err)
		}
	}

	must(os.MkdirAll(filepath.Join(root, "usr", "bin"), 0o750))
	must(os.MkdirAll(filepath.Join(root, "var", "empty"), 0o700))
	must(os.WriteFile(filepath.Join(root, "usr", "bin", "tool"), []byte("#!/bin/sh\n"), 0o750))
	must(os.WriteFile(filepath.Join(root, "usr", "bin", "same"), []byte("#!/bin/sh\n"), 0o600))
	must(os.WriteFile(filepath.Join(root, "readme"), bytes.Repeat([]byte("x"), 4096), 0o600))
	must(os.Symlink("usr/bin/tool", filepath.Join(root, "tool")))

	// A time that is not now, so a restore that forgets mtimes is caught rather
	// than passing because both trees were made in the same second.
	when := time.Unix(1_500_000_000, 123_456_789)
	must(os.Chtimes(filepath.Join(root, "readme"), when, when))

	return root
}

// A packed layer restores to a tree with the same identity.
//
// The property the fleet needs and does not have: a layer is a *directory* and
// the transfer protocol moves *bytes*, so without a codec there is nothing to
// send (E261). "Same identity" rather than "same files" because the identity is
// what a cache key and a base reference are made of - a restore that got the
// contents right and the modes wrong would produce a layer nothing could use.
func TestAPackedLayerRestoresToTheSameIdentity(t *testing.T) {
	t.Parallel()

	root := tree(t)

	want, err := layer.Take(root)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	err = layer.Pack(root, &buf)
	if err != nil {
		t.Fatalf("packing: %v", err)
	}

	into := filepath.Join(t.TempDir(), "restored")

	err = layer.Unpack(&buf, into)
	if err != nil {
		t.Fatalf("unpacking: %v", err)
	}

	got, err := layer.Take(into)
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != want.ID {
		t.Errorf("restored layer is %v, want %v"+
			"\n  a layer that does not restore to its own identity cannot be a"+
			" base: every key derived from it names something else",
			got.ID, want.ID)
	}

	if got.Bytes != want.Bytes {
		t.Errorf("restored %d bytes, want %d", got.Bytes, want.Bytes)
	}
}

// Packing the same tree twice produces the same bytes.
//
// Two machines that pack one layer differently give the fleet as many copies as
// it has senders, none of which share a cache entry - so the transfer would grow
// the cache instead of using it. Determinism here is the same requirement as
// determinism in the digest (I1), for the same reason.
func TestPackingIsDeterministic(t *testing.T) {
	t.Parallel()

	root := tree(t)

	var first, second bytes.Buffer

	for _, w := range []*bytes.Buffer{&first, &second} {
		err := layer.Pack(root, w)
		if err != nil {
			t.Fatal(err)
		}
	}

	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Error("two packs of one tree differ" +
			"\n  directory order or a map's iteration order has reached the" +
			" encoding")
	}
}

// A path that escapes the root is refused.
//
// The stream arrives from a peer (A5). An entry named `../../etc/profile` that
// was written where it asked would let any machine in a fleet write anywhere on
// any other - the single most valuable thing an attacker could get from a build
// system, and it is one missing check away.
func TestAnEscapingPathIsRefused(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{
		"../escape",
		"a/../../escape",
		"/absolute",
	} {
		err := layer.Unpack(bytes.NewReader(packOne(t, bad)), t.TempDir())
		if err == nil {
			t.Errorf("%q was unpacked", bad)
		}
	}
}

// A truncated stream is refused, not half-applied.
//
// Half a layer is not a smaller layer: it is a tree whose digest is something
// nobody asked for, and one that would be filed under the name that *was* asked
// for if the error were swallowed.
func TestATruncatedStreamIsRefused(t *testing.T) {
	t.Parallel()

	root := tree(t)

	var buf bytes.Buffer

	err := layer.Pack(root, &buf)
	if err != nil {
		t.Fatal(err)
	}

	whole := buf.Bytes()

	for _, n := range []int{0, 4, len(whole) / 2, len(whole) - 1} {
		err := layer.Unpack(bytes.NewReader(whole[:n]), t.TempDir())
		if err == nil {
			t.Errorf("a stream cut to %d of %d bytes was accepted", n, len(whole))
		}
	}
}

// packOne builds a stream naming a single directory at path.
//
// Encoded here by hand rather than by calling Pack, deliberately: the paths this
// exercises are ones Pack will never produce, because a walk of a real tree
// cannot yield `../escape`. A hostile stream has to be *written* to be tested,
// and writing it here also means a change to the format that this does not
// follow shows up as a failure rather than as a test that quietly stopped
// exercising anything.
func packOne(t *testing.T, path string) []byte {
	t.Helper()

	var b bytes.Buffer

	e := ir.NewEncoder(&b)

	e.Fixed([]byte("EBLAYER1"))
	e.Count(1)

	e.Str(path)
	e.Byte('d')

	be32 := func(v uint32) []byte {
		var x [4]byte

		binary.BigEndian.PutUint32(x[:], v)

		return x[:]
	}

	be64 := func(v int64) []byte {
		var x [8]byte

		binary.BigEndian.PutUint64(x[:], uint64(v))

		return x[:]
	}

	e.Fixed(be32(0o40755)) // mode
	e.Fixed(be32(0))       // uid
	e.Fixed(be32(0))       // gid
	e.Fixed(be64(0))       // mtime seconds
	e.Fixed(be32(0))       // mtime nanoseconds
	e.Fixed(be64(0))       // size

	var empty ir.NodeID

	e.Fixed(empty[:]) // content digest
	e.Str("")         // link target
	e.Count(0)        // extended attributes
	e.Count(0)        // bodies

	return b.Bytes()
}

// A symlink's own timestamp survives, and its target's is not disturbed.
//
// The bug this catches was the only thing standing between a round trip and a
// matching identity, and it is invisible without comparing digests: `os.Chtimes`
// **follows** a symlink, so stamping one writes the time onto the file it points
// at. Two entries wrong from one call - the link keeps whatever time it was
// created with, and the target gets a time it never had.
func TestALinkIsStampedWithoutFollowingIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.WriteFile(filepath.Join(root, "target"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink("target", filepath.Join(root, "link"))
	if err != nil {
		t.Fatal(err)
	}

	// Distinct times, so a restore that stamped one onto the other is caught
	// rather than hidden by both being the same.
	target := time.Unix(1_400_000_000, 0)
	link := time.Unix(1_600_000_000, 0)

	must(t, os.Chtimes(filepath.Join(root, "target"), target, target))
	must(t, lchtimesForTest(filepath.Join(root, "link"), link))

	want, err := layer.Take(root)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	must(t, layer.Pack(root, &buf))

	into := filepath.Join(t.TempDir(), "restored")

	must(t, layer.Unpack(&buf, into))

	got, err := layer.Take(into)
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != want.ID {
		st, err := os.Lstat(filepath.Join(into, "link"))
		if err == nil {
			t.Logf("restored link mtime %v, want %v", st.ModTime().Unix(), link.Unix())
		}

		ft, err := os.Stat(filepath.Join(into, "target"))
		if err == nil {
			t.Logf("restored target mtime %v, want %v", ft.ModTime().Unix(), target.Unix())
		}

		t.Errorf("restored layer is %v, want %v", got.ID, want.ID)
	}
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

// A length the sender invented is refused before it is believed.
//
// Every count in the stream is a number the *other machine* chose. A four-byte
// field asking for four billion entries costs the sender four bytes and the
// receiver its memory, which is a denial of service with no work behind it - and
// the bound has to be checked before the allocation, not after.
func TestALengthTheSenderInventedIsRefused(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer

	e := ir.NewEncoder(&b)

	e.Fixed([]byte("EBLAYER1"))
	e.Count(1 << 30) // and then nothing at all

	err := layer.Unpack(bytes.NewReader(b.Bytes()), t.TempDir())
	if err == nil {
		t.Fatal("a stream claiming a billion entries was accepted")
	}

	if !errors.Is(err, layer.ErrMalformed) {
		t.Errorf("%v; want ErrMalformed", err)
	}

	// And says which number was wrong. Every allocation from a count is capped
	// anyway, so what the bound buys is a message: without it this fails as
	// "wanted 4 more bytes" after reading everything the stream had, which tells
	// the reader nothing about who chose the number or how big it was.
	if !strings.Contains(err.Error(), "bound") {
		t.Errorf("refused with %q, which does not say a length was out of"+
			" bounds\n  the reader has to be able to tell a hostile length from"+
			" a truncated stream", err)
	}
}
