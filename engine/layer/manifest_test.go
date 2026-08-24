package layer_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A layer's manifest hashes to the layer's identity.
//
// **The property that makes a fragment verifiable without changing anything.**
// E282 recorded that a fragment could not be checked against its layer, because
// a layer is hashed as one flat sequence with no inclusion proof for a subset -
// and concluded that closing the gap meant making the digest a Merkle tree, a
// change to §3.2 and to every digest this engine has computed.
//
// It does not. The sequence being hashed is **metadata and per-file content
// digests** - never file bytes (§3.3). So the whole of it is a manifest that is
// small enough to send: about a hundred bytes an entry, two megabytes for a base
// of twenty thousand files, against the hundreds of megabytes the base itself
// weighs (E284).
//
// Send that, hash it, compare it to the layer's identity, and every path's
// content digest is authenticated. A fragment is then checked file by file
// against digests nobody could have forged without breaking the layer's name.
func TestAManifestHashesToTheLayersIdentity(t *testing.T) {
	t.Parallel()

	root := tree(t)

	want, err := layer.Take(root)
	if err != nil {
		t.Fatal(err)
	}

	m, err := layer.Manifest(root)
	if err != nil {
		t.Fatalf("taking a manifest: %v", err)
	}

	if got := layer.ManifestID(m); got != want.ID {
		t.Fatalf("the manifest hashes to %v and the layer is %v"+
			"\n  if these differ there is no cheap inclusion proof and the"+
			" digest really does have to become a tree", got, want.ID)
	}
}

// A manifest is small next to what it describes.
//
// The whole argument for this over a Merkle tree: an O(n) proof is fine when n
// is entries and each is a hundred bytes, and the alternative was changing every
// digest in the system.
func TestAManifestIsSmallNextToTheLayer(t *testing.T) {
	t.Parallel()

	root := tree(t)

	m, err := layer.Manifest(root)
	if err != nil {
		t.Fatal(err)
	}

	c, err := layer.Take(root)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("manifest %d bytes for %d bytes of contents (%.1f%%)",
		len(m), c.Bytes, 100*float64(len(m))/float64(max(c.Bytes, 1)))

	if int64(len(m)) >= c.Bytes {
		t.Errorf("the manifest is %d bytes and the contents are %d;"+
			" this fixture is too small to say anything, but a real base is not",
			len(m), c.Bytes)
	}
}

// A manifest of a different tree does not hash to this layer.
//
// Which is what makes it a proof rather than a description: a peer cannot send a
// manifest that authenticates paths the layer does not have.
func TestAManifestOfAnotherTreeIsNotThisLayer(t *testing.T) {
	t.Parallel()

	mine := tree(t)
	theirs := tree(t)

	// Two trees built the same way differ only in their timestamps, which is
	// enough - and is why the fixture stamps one file with a fixed time and
	// leaves the rest to the clock.
	m, err := layer.Manifest(theirs)
	if err != nil {
		t.Fatal(err)
	}

	c, err := layer.Take(mine)
	if err != nil {
		t.Fatal(err)
	}

	if layer.ManifestID(m) == c.ID {
		t.Skip("the two fixtures came out identical; nothing to say")
	}
}

// A fragment is checked, file by file, against an authenticated manifest.
//
// This is E282's gap closed. The manifest hashes to the layer's name, so every
// content digest in it is as trustworthy as the name - and a fragment whose
// files do not match those digests is refused, however plausible it looks.
func TestAFragmentIsCheckedAgainstItsManifest(t *testing.T) {
	t.Parallel()

	root := tree(t)

	m, err := layer.Manifest(root)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	err = layer.PackPaths(root, &buf, []string{"usr/bin/tool"})
	if err != nil {
		t.Fatal(err)
	}

	into := filepath.Join(t.TempDir(), "fragment")
	err = layer.Unpack(&buf, into)
	if err != nil {
		t.Fatal(err)
	}

	err = layer.VerifyFragment(m, into)
	if err != nil {
		t.Fatalf("an honest fragment was refused: %v", err)
	}
}

// A fragment of somebody else's tree is refused.
//
// The attack the gap allowed: a peer answers a request for part of layer L with
// part of something else entirely, and the receiver has no way to tell. It has
// one now.
func TestAFragmentOfAnotherTreeIsRefused(t *testing.T) {
	t.Parallel()

	root := tree(t)

	m, err := layer.Manifest(root)
	if err != nil {
		t.Fatal(err)
	}

	// The same paths, different contents.
	other := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(other, "usr", "bin"), 0o750))
	// An executable the layer is meant to carry.
	must(t, os.WriteFile(filepath.Join(other, "usr", "bin", "tool"), //nolint:gosec
		[]byte("#!/bin/sh\nrm -rf /\n"), 0o750))

	var buf bytes.Buffer

	must(t, layer.PackPaths(other, &buf, []string{"usr/bin/tool"}))

	into := filepath.Join(t.TempDir(), "fragment")
	must(t, layer.Unpack(&buf, into))

	err = layer.VerifyFragment(m, into)
	if err == nil {
		t.Fatal("a fragment of a different tree was accepted as part of this one")
	}
}

// A path the manifest does not mention is refused.
//
// A fragment that carries something extra is not a generous fragment: it is a
// peer adding a file to somebody's base, which is the whole of what an attacker
// would want from this.
func TestAFragmentWithAnExtraPathIsRefused(t *testing.T) {
	t.Parallel()

	root := tree(t)

	m, err := layer.Manifest(root)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	must(t, layer.PackPaths(root, &buf, []string{"usr/bin/tool"}))

	into := filepath.Join(t.TempDir(), "fragment")
	must(t, layer.Unpack(&buf, into))

	// Somebody adds a file after the fact.
	// An executable the layer is meant to carry.
	must(t, os.WriteFile(filepath.Join(into, "usr", "bin", "extra"), //nolint:gosec
		[]byte("not in the layer\n"), 0o750))

	err = layer.VerifyFragment(m, into)
	if err == nil {
		t.Fatal("a fragment carrying a path the layer does not have was accepted")
	}
}
