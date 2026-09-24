package guest

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// TestALayerCanBePlacedUnderANameTheCallerChose.
//
// **A build context is not named by what it holds.** An unpacked image layer is
// filed under the digest of its own tree, and that is right - two images
// sharing a layer share the file. A context is filed under the identity the
// *plan* gave it, computed when the interpreter digested the host directory,
// because that identity is already in the cache key of every step that copies
// from it.
//
// The host does this with `PutNamed`. With the store on the guest's device the
// host cannot: `Publish` renames into place and a rename does not cross a
// filesystem, so a tree staged on the host can never become a layer in the
// guest's store (E690). The guest has to do the placing, which means it has to
// be told the name.
func TestALayerCanBePlacedUnderANameTheCallerChose(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)
	body := []byte("from the context")

	err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "src/main.go", Mode: 0o644, Size: int64(len(body)),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tw.Write(body)
	if err != nil {
		t.Fatal(err)
	}

	err = tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	blob := filepath.Join(dir, "context.tar")

	err = os.WriteFile(blob, buf.Bytes(), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	want := ir.NodeID{7, 7, 7}
	s := &Server{LayerDir: t.TempDir()}

	resp := s.unpackLayer(Request{
		Kind: KindUnpackLayer, Blob: blob,
		Media: "application/vnd.oci.image.layer.v1.tar",
		As:    want.String(),
	})
	if resp.Err != "" {
		t.Fatalf("unpacking under a chosen name: %s", resp.Err)
	}

	if resp.Layer != want.String() {
		t.Errorf("the guest filed it as %s, want %s", resp.Layer, want)
	}

	// And it is really there, under that name, with the content.
	st := store.DirStore(s.LayerDir)
	if !st.Has(want) {
		t.Fatalf("the store does not hold %s after placing it there", want)
	}

	got, err := os.ReadFile(filepath.Join(st.LayerPath(want), "src", "main.go"))
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != string(body) {
		t.Errorf("the placed layer holds %q, want %q", got, body)
	}
}
