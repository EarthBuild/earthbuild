package guest_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/klauspost/compress/gzip"
)

// TestTheGuestCanUnpackABlobIntoItsOwnStore.
//
// **The store is on the wrong side of virtiofs and this is the first piece of
// moving it.** E511 established the principle and acted on half of it: a CACHE
// mount lives on the block device the guest owns, because "outliving the build
// does not mean the *host* must see it". The layer store never moved, and it is
// where the reading happens.
//
// Measured on the same layer, from inside the guest: unpacking into the shared
// store takes 4.67s and into the volume 2.18s, and reading all of it back 6.04s
// against 1.47s cold - 0.31ms per file opened, which a step pays on every file
// of its base.
//
// The host cannot write the volume, so the unpack has to be asked for rather
// than done. This is that request: a blob the guest can read, unpacked and
// placed, with the layer's name reported back.
func TestTheGuestCanUnpackABlobIntoItsOwnStore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	blob := filepath.Join(dir, "layer.tar.gz")

	err := os.WriteFile(blob, aGzippedLayerBlob(t), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()

	c := pairWith(t, &guest.Server{LayerDir: root})

	id, err := c.UnpackLayer(context.Background(), blob,
		"application/vnd.oci.image.layer.v1.tar+gzip")
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}

	if id == (ir.NodeID{}) {
		t.Fatal("the guest unpacked a layer and did not say what it is called")
	}

	st := store.DirStore(root)
	if !st.Has(id) {
		t.Fatalf("the store does not hold %v, which it just reported placing", id)
	}

	body, err := os.ReadFile(filepath.Join(st.LayerPath(id), "etc", "conf"))
	if err != nil {
		t.Fatal(err)
	}

	if string(body) != "key=value" {
		t.Errorf("the placed layer holds %q", body)
	}

	// **And it is the name the archive attests to.** A layer the guest placed
	// under some other id could never be found by a host that named it from the
	// blob, which is how the host will name one once it stops unpacking.
	want, err := layer.ManifestFromTar(bytes.NewReader(aPlainLayerTar(t)))
	if err != nil {
		t.Fatal(err)
	}

	if got := layer.ManifestID(want); got != id {
		t.Errorf("the guest placed %v and the archive attests to %v", id, got)
	}
}

// TestUnpackingWithoutAStoreSaysSo: `DirStore("")` joins to a relative path, so
// an unset store would place a layer wherever this process happens to be - the
// same reason store-has refuses.
func TestUnpackingWithoutAStoreSaysSo(t *testing.T) {
	t.Parallel()

	c := pairWith(t, &guest.Server{})

	_, err := c.UnpackLayer(context.Background(), "/nowhere",
		"application/vnd.oci.image.layer.v1.tar+gzip")
	if err == nil {
		t.Fatal("a guest with no layer directory unpacked into one anyway")
	}
}

func aPlainLayerTar(t *testing.T) []byte {
	t.Helper()

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

	return buf.Bytes()
}

func aGzippedLayerBlob(t *testing.T) []byte {
	t.Helper()

	var gz bytes.Buffer

	zw := gzip.NewWriter(&gz)

	_, err := zw.Write(aPlainLayerTar(t))
	if err != nil {
		t.Fatal(err)
	}

	err = zw.Close()
	if err != nil {
		t.Fatal(err)
	}

	return gz.Bytes()
}

// TestAPlacedLayerCarriesTheImagesDeclaration.
//
// A layer the guest places has to carry everything a layer carries, and the
// configuration is the part the host used to file itself with `AdoptConfig` -
// which it cannot do on a device it does not have.
//
// The property that matters is not that a file arrived. It is that the
// declaration the store derives from it is the one the host would have derived
// from the same configuration in hand (`DeclarationOf`), because a stack element
// named one way and looked up the other is two elements for one image (§3.2a).
func TestAPlacedLayerCarriesTheImagesDeclaration(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	blob := filepath.Join(dir, "layer.tar.gz")

	err := os.WriteFile(blob, aGzippedLayerBlob(t), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	cfg := ocispec.ImageConfig{
		Env:        []string{"PATH=/usr/local/bin:/usr/bin", "LANG=C.UTF-8"},
		WorkingDir: "/src",
		Entrypoint: []string{"/entry"},
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	c := pairWith(t, &guest.Server{LayerDir: root})

	id, err := c.UnpackLayerWithConfig(context.Background(), blob,
		"application/vnd.oci.image.layer.v1.tar+gzip", raw)
	if err != nil {
		t.Fatal(err)
	}

	got := store.DirStore(root).Declaration(id)

	want := store.DeclarationOf(cfg)
	if want == (ir.NodeID{}) {
		t.Fatal("the fixture declares nothing, so this test would pass vacuously")
	}

	if got != want {
		t.Fatalf("the placed layer declares %v and the configuration says %v", got, want)
	}
}

// TestALayerWithNoConfigurationDeclaresNothing: an image that says nothing is
// the ordinary case, and it must not leave an empty sidecar that later reads as
// a declaration of emptiness.
func TestALayerWithNoConfigurationDeclaresNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	blob := filepath.Join(dir, "layer.tar.gz")

	err := os.WriteFile(blob, aGzippedLayerBlob(t), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	c := pairWith(t, &guest.Server{LayerDir: root})

	id, err := c.UnpackLayer(context.Background(), blob,
		"application/vnd.oci.image.layer.v1.tar+gzip")
	if err != nil {
		t.Fatal(err)
	}

	if got := store.DirStore(root).Declaration(id); got != (ir.NodeID{}) {
		t.Errorf("a layer placed without a configuration declares %v", got)
	}
}
