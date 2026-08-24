package layer_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A fragment with the right bytes and the wrong mode is refused.
//
// **A fragment was authenticated on contents alone.** The manifest carries every
// field of green paper §3.3 - kind, mode, ownership, times, size, device - and
// the reader discarded all of them (`_ = d.fixed(40)`) to keep the content
// digest. So a peer could send a file with the right bytes and mode 0777, and
// the step would read something the layer does not describe.
//
// It matters more now than when it was written: since E323 the lazy path is the
// one that wins, so this is the check standing between a fleet and a wrong
// build, not a corner (§5.3, I2).
//
// Two fields are deliberately outside the seal and both are stated rather than
// forgotten - see `TestAFragmentIsNotSealedOnWhatItCannotReproduce`.
func TestAFragmentWithTheWrongModeIsRefused(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	at := filepath.Join(src, "a.txt")

	err := os.WriteFile(at, []byte("hello"), 0o600)
	if err != nil {
		t.Fatalf("%v", err)
	}

	m, err := layer.Manifest(src)
	if err != nil {
		t.Fatalf("%v", err)
	}

	// The bytes a lying peer sends: same content, different mode.
	if err = os.Chmod(at, 0o750); err != nil {
		t.Fatalf("%v", err)
	}

	err = layer.VerifyFragment(m, src)
	if err == nil {
		t.Error("a fragment whose file is world-writable passed as one that is" +
			" not\n  the manifest carries the mode and the check threw it away" +
			" (E324)")
	}
}

// A fragment is not sealed on what the receiver cannot reproduce.
//
// **Two fields, both by argument rather than by omission.**
//
// *Ownership*, because restoring it needs privilege a worker does not have: the
// same fact that made a whole layer capture under the wrong digest (E313). The
// manifest's own declaration is what a fragment is judged by, so a peer cannot
// lie about it usefully - it is simply not what the disk is compared against.
//
// *Hardlinks*, because a fragment is a subset and a link's partner may not be in
// it. A seal over that field would refuse honest fragments of any layer
// containing a hardlink, which is every layer built from a package manager.
func TestAFragmentIsNotSealedOnWhatItCannotReproduce(t *testing.T) {
	// **Not parallel**, because it swaps a package variable - the rule written
	// beside `ObservedOwnerForTest` and broken by the next test to use it. It
	// passed alone and failed in a full run, corrupting an unrelated symlink
	// test, which is what a global seam does when it escapes.
	src := t.TempDir()

	err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hi"), 0o600)
	if err != nil {
		t.Fatalf("%v", err)
	}

	m, err := layer.Manifest(src)
	if err != nil {
		t.Fatalf("%v", err)
	}

	// The tree as an unprivileged worker would have it: right bytes, right
	// mode, ownership it could not set.
	layer.ObservedOwnerForTest(t, func(uid, gid uint32) (uint32, uint32) {
		return uid + 1, gid + 1
	})

	err = layer.VerifyFragment(m, src)
	if err != nil {
		t.Errorf("%v\n  a worker that cannot chown cannot use a lazy base"+
			" at all, which is the whole of E313 again", err)
	}
}

// The manifest a fragment is checked against is still the one it was sent with.
//
// Guards the seal from being made vacuous: a check that read its expectations
// out of the same tree it is checking would pass anything.
func TestAFragmentIsCheckedAgainstTheManifestNotItself(t *testing.T) {
	t.Parallel()

	src := t.TempDir()

	err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hi"), 0o600)
	if err != nil {
		t.Fatalf("%v", err)
	}

	other := t.TempDir()

	err = os.WriteFile(filepath.Join(other, "a.txt"), []byte("bye"), 0o600)
	if err != nil {
		t.Fatalf("%v", err)
	}

	m, err := layer.Manifest(other)
	if err != nil {
		t.Fatalf("%v", err)
	}

	err = layer.VerifyFragment(m, src)
	if err == nil {
		t.Error("a fragment passed against another layer's manifest")
	}
}

// A manifest whose kind disagrees with its own mode is malformed.
//
// The kind byte travels beside the mode and the seal re-derives it from the
// mode on both sides - so the byte would go unused, and an unused field on the
// wire is a field a peer can set to anything. It is checked rather than
// discarded.
func TestAManifestWhoseKindContradictsItsModeIsRefused(t *testing.T) {
	t.Parallel()

	src := t.TempDir()

	err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hi"), 0o600)
	if err != nil {
		t.Fatalf("%v", err)
	}

	m, err := layer.Manifest(src)
	if err != nil {
		t.Fatalf("%v", err)
	}

	// The kind byte follows the path, which is a length-prefixed string.
	// Finding it by searching for the value rather than by offset arithmetic:
	// 'f' is the only such byte before the fixed block here.
	i := bytes.IndexByte(m, 'f')
	if i < 0 {
		t.Fatal("no kind byte in a manifest of one regular file")
	}

	m[i] = 'd'

	err = layer.VerifyFragment(m, src)
	if err == nil {
		t.Error("a manifest calling a regular file a directory was accepted")
	}
}
