package exec_test

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"

	"github.com/klauspost/compress/gzip"
)

// TestALayerPlacedFromABlobCanBeServedFromItAgain.
//
// **The whole chain, composed.** A pull keeps a layer's compressed bytes
// (E659), the store records which layer they unpack to, and `fleet.Blobs`
// answers a fragment request from them without the tree - E656 names the layer
// from the archive, E657 packs part of it, E658 measured the pair at 76% of an
// unpack-and-name.
//
// Each piece has its own test. This one asserts they meet: the id the store
// filed the layer under is the id the blob attests to, and a blob that attested
// to anything else would be refused rather than served.
func TestALayerPlacedFromABlobCanBeServedFromItAgain(t *testing.T) {
	t.Parallel()

	plain, compressed := aGzippedLayer(t)

	root := t.TempDir()
	st := store.DirStore(root)

	// The layer, unpacked and placed exactly as a pull would.
	staging, err := st.Staging(".apart-")
	if err != nil {
		t.Fatal(err)
	}

	got, err := image.UnpackApart(bytes.NewReader(plain), staging)
	if err != nil {
		t.Fatal(err)
	}

	own := map[string]layer.Owner{}
	for at, o := range got.Owners {
		own[at] = layer.Owner{UID: o.UID, GID: o.GID}
	}

	id, err := st.PlaceAs(staging, store.Placement{Owners: own})
	if err != nil {
		t.Fatal(err)
	}

	// And its blob, filed and joined.
	blobs, err := blob.New(filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatal(err)
	}

	at, _, err := blobs.Put(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}

	st.NoteBlob(id, at, "application/vnd.oci.image.layer.v1.tar+gzip")

	// A source that finds its layers by the note the pull left.
	source := &fleet.Blobs{Store: blobs, Of: st.BlobOf}

	manifest, packed, err := source.Fragment(
		context.Background(), id, []string{"etc/conf"}, true)
	if err != nil {
		t.Fatalf("the blob the pull kept cannot serve the layer it unpacked to: %v", err)
	}

	if packed == nil {
		t.Fatal("the join produced no fragment, so the note was not found")
	}

	if layer.ManifestID(manifest) != id {
		t.Fatalf("the blob attests to %v and the store filed the layer as %v",
			layer.ManifestID(manifest), id)
	}

	// And it restores to the path that was asked for, checked against the proof
	// the same blob wrote.
	into := t.TempDir()

	err = layer.Unpack(bytes.NewReader(packed), into)
	if err != nil {
		t.Fatal(err)
	}

	err = layer.VerifyFragment(manifest, into)
	if err != nil {
		t.Fatalf("the fragment does not check against its own proof: %v", err)
	}

	_, err = os.Stat(filepath.Join(into, "etc", "conf"))
	if err != nil {
		t.Errorf("the path that was asked for is not in the fragment: %v", err)
	}
}

// aGzippedLayer is a small layer, plain and compressed.
func aGzippedLayer(t *testing.T) (plain, compressed []byte) {
	t.Helper()

	when := time.Unix(1700000000, 123456789)

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	for _, e := range []struct{ name, body string }{
		{"etc/conf", "key=value"},
		{"usr/bin/tool", "the tool"},
	} {
		err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: e.name, Mode: 0o644,
			Size: int64(len(e.body)), ModTime: when,
		})
		if err != nil {
			t.Fatal(err)
		}

		_, err = tw.Write([]byte(e.body))
		if err != nil {
			t.Fatal(err)
		}
	}

	err := tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	var gz bytes.Buffer

	zw := gzip.NewWriter(&gz)

	_, err = zw.Write(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	err = zw.Close()
	if err != nil {
		t.Fatal(err)
	}

	return buf.Bytes(), gz.Bytes()
}
