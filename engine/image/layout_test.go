package image_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// layers writes two directories to stand in for a stack.
func layers(t *testing.T) []image.LayerSource {
	t.Helper()

	return image.FromDirs([]string{
		tree(t, map[string]string{"bin/sh": "the base\n"}),
		tree(t, map[string]string{"app/main": "what the build made\n"}),
	})
}

// An image is written as an OCI layout that other tools can read.
//
// The layout is the interchange format: `docker load`, `skopeo copy`, `crane
// push` and a registry all start here. Writing something almost-but-not-quite
// like it would produce a directory that only this engine understands, which is
// the opposite of the point.
func TestWriteLayoutProducesAReadableImage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := image.WriteLayout(dir, image.Spec{
		Ref:      testImageRef,
		Platform: ocispec.Platform{OS: testOS, Architecture: testArch},
		Layers:   layers(t),
		Config: ocispec.ImageConfig{
			Entrypoint: []string{testBinary},
			Env:        []string{"PATH=/usr/bin"},
			WorkingDir: testWorkdir,
			Labels:     map[string]string{"org.example.built-by": "earthbuild"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// oci-layout, saying which version of the format this is.
	var marker ocispec.ImageLayout

	readJSON(t, filepath.Join(dir, "oci-layout"), &marker)

	if marker.Version != ocispec.ImageLayoutVersion {
		t.Errorf("oci-layout says version %q, want %q", marker.Version, ocispec.ImageLayoutVersion)
	}

	// index.json, naming the image so a tool knows what to call it.
	var index ocispec.Index

	readJSON(t, filepath.Join(dir, "index.json"), &index)

	if len(index.Manifests) != 1 {
		t.Fatalf("the index holds %d manifests, want 1", len(index.Manifests))
	}

	if got := index.Manifests[0].Annotations[ocispec.AnnotationRefName]; got != testImageRef {
		t.Errorf("the index calls the image %q, want app:latest", got)
	}

	// The manifest, and every blob it names present.
	var manifest ocispec.Manifest

	readJSON(t, blobPath(t, dir, index.Manifests[0].Digest.String()), &manifest)

	if len(manifest.Layers) != 2 {
		t.Fatalf("the manifest lists %d layers, want 2", len(manifest.Layers))
	}

	for _, d := range append([]ocispec.Descriptor{manifest.Config}, manifest.Layers...) {
		p := blobPath(t, dir, d.Digest.String())

		fi, err := os.Stat(p)
		if err != nil {
			t.Errorf("the manifest names a blob that is not there: %v", err)

			continue
		}

		if fi.Size() != d.Size {
			t.Errorf("%s says %d bytes, the blob is %d", d.Digest, d.Size, fi.Size())
		}
	}

	// The config carries what SAVE IMAGE declared, and the diff ids match the
	// layers - a config whose rootfs disagrees with the manifest is an image
	// that pulls and then will not start.
	var config ocispec.Image

	readJSON(t, blobPath(t, dir, manifest.Config.Digest.String()), &config)

	if len(config.RootFS.DiffIDs) != len(manifest.Layers) {
		t.Errorf("the config names %d layers, the manifest %d",
			len(config.RootFS.DiffIDs), len(manifest.Layers))
	}

	if len(config.Config.Entrypoint) != 1 || config.Config.Entrypoint[0] != testBinary {
		t.Errorf("the entrypoint is %v, want /app/main", config.Config.Entrypoint)
	}

	if config.Config.WorkingDir != testWorkdir {
		t.Errorf("the working directory is %q, want /app", config.Config.WorkingDir)
	}

	if config.Platform.Architecture != testArch || config.Platform.OS != testOS {
		t.Errorf("the config says %s/%s, want linux/arm64", config.Platform.OS, config.Platform.Architecture)
	}
}

// Writing the same image twice produces the same digests.
//
// The manifest digest is what a registry stores and what a deployment pins, so
// an image that changes identity without changing content republishes the world
// for nothing.
func TestWritingAnImageIsReproducible(t *testing.T) {
	t.Parallel()

	src := layers(t)

	spec := image.Spec{
		Ref:      testImageRef,
		Platform: ocispec.Platform{OS: testOS, Architecture: testArch},
		Layers:   src,
		Config:   ocispec.ImageConfig{Entrypoint: []string{testBinary}},
	}

	digestOf := func() string {
		dir := t.TempDir()

		err := image.WriteLayout(dir, spec)
		if err != nil {
			t.Fatal(err)
		}

		var index ocispec.Index

		readJSON(t, filepath.Join(dir, "index.json"), &index)

		return index.Manifests[0].Digest.String()
	}

	if first, second := digestOf(), digestOf(); first != second {
		t.Errorf("two writes of one image produced %s and %s", first, second)
	}
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}

	err = json.Unmarshal(b, into)
	if err != nil {
		t.Fatalf("parse %s: %v", filepath.Base(path), err)
	}
}

// blobPath is where a layout keeps a blob: blobs/<algorithm>/<hex>.
func blobPath(t *testing.T, dir, digest string) string {
	t.Helper()

	alg, hex, ok := strings.Cut(digest, ":")
	if !ok {
		t.Fatalf("%q is not a digest", digest)
	}

	return filepath.Join(dir, "blobs", alg, hex)
}

// An OCI tool that is not this one can read what was written.
//
// The tests above check the layout with the same types that wrote it, which
// proves it is self-consistent and nothing more. `skopeo` is an independent
// reader with no interest in what this engine believes, and its opinion is the
// one that matters: the layout exists to be handed to something else.
func TestSkopeoCanReadTheLayout(t *testing.T) {
	t.Parallel()

	skopeo, err := osexec.LookPath("skopeo")
	if err != nil {
		t.Skip("skopeo is not installed")
	}

	dir := t.TempDir()

	err = image.WriteLayout(dir, image.Spec{
		Ref:      testImageRef,
		Platform: ocispec.Platform{OS: testOS, Architecture: testArch},
		Layers:   layers(t),
		Config: ocispec.ImageConfig{
			Entrypoint: []string{testBinary},
			Labels:     map[string]string{"org.example.built-by": "earthbuild"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	out, err := osexec.CommandContext(ctx, skopeo, "inspect", "--raw", "oci:"+dir+":app:latest").Output()
	if err != nil {
		if ee, ok := errors.AsType[*osexec.ExitError](err); ok {
			t.Fatalf("skopeo refused the layout: %v\n%s", err, ee.Stderr)
		}

		t.Fatalf("skopeo refused the layout: %v", err)
	}

	var manifest ocispec.Manifest
	err = json.Unmarshal(out, &manifest)
	if err != nil {
		t.Fatalf("skopeo returned something that is not a manifest: %v", err)
	}

	if len(manifest.Layers) != 2 {
		t.Errorf("skopeo sees %d layers, want 2", len(manifest.Layers))
	}

	if manifest.Config.MediaType != ocispec.MediaTypeImageConfig {
		t.Errorf("skopeo sees a config of type %q", manifest.Config.MediaType)
	}
}
