package image_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// A packed image says when it was made.
//
// Every image in a registry carries `created`, and ours carried nothing:
// `docker inspect` reported an empty string where alpine reports a timestamp,
// which is what `docker image ls` reads for its age column and what a scanner
// reads to decide whether an image is stale (E772).
//
// Given rather than taken from the clock, so a build asked to be reproducible
// stays reproducible: `SOURCE_DATE_EPOCH` fixes this field exactly as it fixes
// every file's mtime (E764), and without it this moves like everything else a
// build stamps with the time it happened.
func TestAPackedImageSaysWhenItWasMade(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	from := t.TempDir()
	when := time.Unix(1700000000, 0).UTC()

	err := os.WriteFile(filepath.Join(from, "marker"), []byte("hi\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = image.WriteLayout(filepath.Join(dir, "img"), image.Spec{
		Ref:      "test:img",
		Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"},
		Layers:   []image.LayerSource{image.FromDir(from)},
		Created:  when,
	})
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "img", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}

	var manifest []struct{ Config string }

	err = json.Unmarshal(b, &manifest)
	if err != nil || len(manifest) != 1 {
		t.Fatalf("manifest.json: %v", err)
	}

	b, err = os.ReadFile(filepath.Join(dir, "img", manifest[0].Config))
	if err != nil {
		t.Fatal(err)
	}

	var cfg ocispec.Image

	err = json.Unmarshal(b, &cfg)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Created == nil {
		t.Fatal("the image config carries no created time")
	}

	if !cfg.Created.Equal(when) {
		t.Errorf("created = %v, want the %v it was given", cfg.Created, when)
	}
}
