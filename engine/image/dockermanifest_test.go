package image_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// A packed image carries the manifest docker's classic image store reads.
//
// The layout is OCI - `oci-layout`, `index.json`, `blobs/sha256/…` - and
// docker's classic store cannot load one. Its loader falls back to the format
// that predates `manifest.json`, treats every top-level directory as a layer,
// and asks for `blobs/json`:
//
//	open /var/lib/docker/tmp/docker-import-703003986/blobs/json:
//	  no such file or directory
//
// which is what every `WITH DOCKER --load` reported. `docker save` writes both -
// OCI blobs *and* a legacy `manifest.json` naming the same blob paths - and that
// is what makes its output loadable by either store (E769).
func TestAPackedImageCarriesTheLegacyManifest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	from := t.TempDir()

	err := os.WriteFile(filepath.Join(from, "marker"), []byte("hi\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = image.WriteLayout(filepath.Join(dir, "img"), image.Spec{
		Ref:      "test:img",
		Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"},
		Layers:   []image.LayerSource{image.FromDir(from)},
	})
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "img", "manifest.json"))
	if err != nil {
		t.Fatalf("the layout has no manifest.json, so docker's classic store"+
			" cannot load it: %v", err)
	}

	var got []struct {
		Config   string
		RepoTags []string
		Layers   []string
	}

	err = json.Unmarshal(b, &got)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 {
		t.Fatalf("manifest.json describes %d images, want 1", len(got))
	}

	// The tag, so `docker load` names the image rather than leaving it dangling.
	if len(got[0].RepoTags) != 1 || got[0].RepoTags[0] != "test:img" {
		t.Errorf("RepoTags = %v, want [test:img]", got[0].RepoTags)
	}

	// Paths into the same blobs the OCI side uses - not copies of them, which
	// is what makes carrying both formats free.
	for _, p := range append([]string{got[0].Config}, got[0].Layers...) {
		if !strings.HasPrefix(p, "blobs/sha256/") {
			t.Errorf("%q does not point into the shared blobs", p)

			continue
		}

		if _, statErr := os.Stat(filepath.Join(dir, "img", p)); statErr != nil {
			t.Errorf("manifest.json names %q, which is not in the layout: %v", p, statErr)
		}
	}

	if len(got[0].Layers) == 0 {
		t.Error("manifest.json names no layers, so the image loads empty")
	}
}
