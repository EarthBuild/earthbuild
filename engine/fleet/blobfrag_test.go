package fleet_test

import (
	"archive/tar"
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"

	"github.com/klauspost/compress/gzip"
)

// aCompressedLayer is a gzipped tar with the shapes that matter, and the layer
// id the archive attests to.
func aCompressedLayer(t *testing.T) (compressed []byte, id ir.NodeID) {
	t.Helper()

	when := time.Unix(1700000000, 123456789)

	var plain bytes.Buffer

	tw := tar.NewWriter(&plain)

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
	write(&tar.Header{Typeflag: tar.TypeSymlink, Name: "usr/link", Linkname: "bin/tool", Mode: 0o777}, "")
	write(&tar.Header{Typeflag: tar.TypeReg, Name: "etc/conf", Mode: 0o600}, "key=value")

	err := tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	var gz bytes.Buffer

	zw := gzip.NewWriter(&gz)

	_, err = zw.Write(plain.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	err = zw.Close()
	if err != nil {
		t.Fatal(err)
	}

	m, err := layer.ManifestFromTar(bytes.NewReader(plain.Bytes()))
	if err != nil {
		t.Fatal(err)
	}

	return gz.Bytes(), layer.ManifestID(m)
}

// TestABlobCanServeAFragmentOfALayerNobodyUnpacked.
//
// **The last piece of a lazy pull.** `Filler` already turns a step's open into a
// fetch and `Prime` materialises a predicted read set; both ask a `Fragmenter`,
// and every existing one packs from a tree in the store. This one packs from the
// compressed blob - 61MB rather than 228MB for `golang:1.26-alpine`'s dominant
// layer, and without the 15034 file creations E654 measured at roughly 78% of
// the unpack.
func TestABlobCanServeAFragmentOfALayerNobodyUnpacked(t *testing.T) {
	t.Parallel()

	compressed, want := aCompressedLayer(t)

	store, err := blob.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	at, _, err := store.Put(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}

	blobs := &fleet.Blobs{
		Store: store,
		Of: func(id ir.NodeID) (ir.NodeID, string, bool) {
			if id != want {
				return ir.NodeID{}, "", false
			}

			return at, "application/vnd.oci.image.layer.v1.tar+gzip", true
		},
	}

	manifest, packed, err := blobs.Fragment(context.Background(), want, []string{"etc/conf"}, true)
	if err != nil {
		t.Fatal(err)
	}

	if got := layer.ManifestID(manifest); got != want {
		t.Fatalf("the blob attests to layer %v, and was asked about %v", got, want)
	}

	// The fragment restores, and to the paths that were asked for.
	root := t.TempDir()

	err = layer.Unpack(bytes.NewReader(packed), root)
	if err != nil {
		t.Fatalf("the fragment does not restore: %v", err)
	}

	err = layer.VerifyFragment(manifest, root)
	if err != nil {
		t.Fatalf("the fragment does not check against the manifest the same blob wrote: %v", err)
	}
}

// TestALayerTheBlobStoreDoesNotHaveIsAMiss: a source that cannot answer says so,
// and `ProvisionFragments` moves to the next one. An error here would fail a
// build over a cache that merely does not have something.
func TestALayerTheBlobStoreDoesNotHaveIsAMiss(t *testing.T) {
	t.Parallel()

	store, err := blob.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	blobs := &fleet.Blobs{
		Store: store,
		Of:    func(ir.NodeID) (ir.NodeID, string, bool) { return ir.NodeID{}, "", false },
	}

	_, packed, err := blobs.Fragment(context.Background(), ir.NodeID{1}, []string{"etc/conf"}, true)
	if err != nil {
		t.Fatalf("a layer this store has never heard of is a miss, not a failure: %v", err)
	}

	if packed != nil {
		t.Error("a miss produced a fragment")
	}
}
