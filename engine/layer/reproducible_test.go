package layer_test

import (
	"archive/tar"
	"bytes"
	"syscall"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// TestTheSameArchiveUnpacksToTheSameLayer.
//
// **A layer's name is a promise about its bytes, so nothing about *when* it was
// unpacked may reach it.** An archive that names `etc/conf` without naming
// `etc/` leaves the unpacker to create the parent, which it does with the
// wall-clock time of that moment - and §3.3 counts an mtime as part of the
// layer, so the same archive was producing a different layer every time.
//
// The consequences are all cache: two machines pulling one image disagree about
// what they hold, a fleet peer cannot serve a layer anybody asked for by name,
// and a re-pull after a cache wipe hits nothing it should have hit.
//
// Real base images usually name their directories, which is why this went
// unnoticed - "usually" not being a property anything should rest on.
func TestTheSameArchiveUnpacksToTheSameLayer(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	// The parent `etc/` is deliberately absent: that is the whole case.
	err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "etc/conf", Mode: 0o600,
		Size: 9, ModTime: time.Unix(1700000000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tw.Write([]byte("key=value"))
	if err != nil {
		t.Fatal(err)
	}

	err = tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	first := unpackID(t, buf.Bytes())

	// A second apart, so a directory stamped with the moment of its creation
	// cannot coincide with the first by luck of the clock's resolution.
	time.Sleep(1100 * time.Millisecond)

	second := unpackID(t, buf.Bytes())

	if first != second {
		t.Fatalf("the same archive unpacked to two layers:\n  %v\n  %v\n"+
			"  something outside the archive is reaching the digest, and the only\n"+
			"  thing that changed between the two is the clock", first, second)
	}
}

func unpackID(t *testing.T, blob []byte) string {
	t.Helper()

	root := t.TempDir()

	_, err := image.UnpackApart(bytes.NewReader(blob), root)
	if err != nil {
		t.Fatal(err)
	}

	c, err := layer.TakeOwnedIn(root, layer.IDMap{}, layer.IDMap{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	return c.ID.String()
}

// TestTheProcessUmaskDoesNotReachTheLayerId is the same defect with a different
// variable.
//
// `os.MkdirAll` applies the umask, so a directory the archive did not describe
// took its mode from whatever the calling process happened to be set to - and a
// mode is part of the layer (§3.3). One machine with umask 022 and one with 077
// pulled the same image and named it differently, which is the whole of what a
// content-addressed store must never do.
//
// Not parallel: the umask is process-wide, so this test cannot share a process
// with anything that creates a file.
//
//nolint:paralleltest // the umask is process state
func TestTheProcessUmaskDoesNotReachTheLayerId(t *testing.T) {
	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "etc/conf", Mode: 0o600,
		Size: 9, ModTime: time.Unix(1700000000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tw.Write([]byte("key=value"))
	if err != nil {
		t.Fatal(err)
	}

	err = tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	was := syscall.Umask(0o077)
	defer syscall.Umask(was)

	tight := unpackID(t, buf.Bytes())

	syscall.Umask(0o022)

	loose := unpackID(t, buf.Bytes())

	if tight != loose {
		t.Fatalf("the umask reached the layer id:\n  077 -> %s\n  022 -> %s\n"+
			"  a directory the archive did not describe must not take its mode\n"+
			"  from whichever process happened to unpack it", tight, loose)
	}
}

// TestTheUnpackingUsersOwnershipDoesNotReachTheLayerId.
//
// **An unprivileged unpack cannot grant the archive's ownership, and that must
// not change what the layer is called.** `engine/image`'s `applyOwner` attempts
// the chown and tolerates EPERM (A2, E92), so on a developer's machine every
// entry ends up the builder's while the same image unpacked as root in a guest
// keeps the archive's. Two ids, one image.
//
// The remedy is already built and is what `TakeOwnedIn`'s declaration parameter
// is for: a layer's own account of who owns it, applied before hashing, so the
// digest is "the one the store would produce rather than the one this namespace
// happens to see" (E313).
//
// Worse than the privilege case and found by accident: on BSD a new file takes
// the *enclosing directory's* group, not the process's, so the id depended on
// where the store happened to live.
//
//nolint:paralleltest // ObservedOwnerForTest is a package variable
func TestTheUnpackingUsersOwnershipDoesNotReachTheLayerId(t *testing.T) {
	blob := aLayerTar(t)

	root := t.TempDir()

	// **The declaration comes from the unpacker, not from the test.** It is the
	// archive's own account of who owns each path, which is the only account
	// that is the same on every machine - and a hand-written map here would
	// prove that `declared()` works, which was never in doubt, rather than that
	// a pulled layer uses it.
	got, err := image.UnpackApart(bytes.NewReader(blob), root)
	if err != nil {
		t.Fatal(err)
	}

	declared := map[string]layer.Owner{}
	for at, o := range got.Owners {
		declared[at] = layer.Owner{UID: o.UID, GID: o.GID}
	}

	if len(declared) == 0 {
		t.Fatal("the unpack declared no ownership, so nothing settles who owns\n" +
			"  a layer and the answer is whoever happened to unpack it")
	}

	asBuilder, err := layer.TakeOwnedIn(root, layer.IDMap{}, layer.IDMap{}, declared)
	if err != nil {
		t.Fatal(err)
	}

	// The same tree, as a machine where the unpack ran as somebody else would
	// report it.
	layer.ObservedOwnerForTest(t, func(uint32, uint32) (uint32, uint32) { return 4242, 4242 })

	asStranger, err := layer.TakeOwnedIn(root, layer.IDMap{}, layer.IDMap{}, declared)
	if err != nil {
		t.Fatal(err)
	}

	if asBuilder.ID != asStranger.ID {
		t.Fatalf("who unpacked the layer reached its name:\n  %v\n  %v\n"+
			"  the declaration is meant to settle this before the digest is taken",
			asBuilder.ID, asStranger.ID)
	}
}
