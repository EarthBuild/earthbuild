package image

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Layers are compressed on the way into an image.
//
// **Measured, because the size of this is easy to underrate.** The base layer of
// a `rust:slim-bookworm` image is 898 MB packed and 286 MB under zstd - so an
// uncompressed push moves three times the bytes for under a second of CPU per
// gigabyte. On a Rust workspace whose `target/` is tens of gigabytes, that
// difference is the whole viability of publishing a build tree.
func TestALayerIsCompressedIntoAnImage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	body := strings.Repeat("compressible, and a registry has to carry it. ", 4096)

	err := WriteLayout(dir, Spec{
		Ref:    "app:latest",
		Layers: []LayerSource{func(w io.Writer) error { _, err := io.WriteString(w, body); return err }},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	m := manifestOf(t, dir)
	layer := m.Layers[0]

	if layer.MediaType != ocispec.MediaTypeImageLayerGzip {
		t.Errorf("layer media type %q, wanted the compressed one", layer.MediaType)
	}

	if layer.Size >= int64(len(body)) {
		t.Errorf("the layer is %d bytes for %d of input, so nothing was compressed",
			layer.Size, len(body))
	}

	// **The descriptor names the compressed bytes and the diffID the plain
	// ones.** They are two different digests of two different things, and a
	// runtime that unpacks the layer checks the second against what it
	// decompressed - so writing one where the other belongs produces an image
	// that pulls and then fails to verify.
	cfg := configOf(t, dir, m.Config.Digest.String())
	if len(cfg.RootFS.DiffIDs) != 1 {
		t.Fatalf("got %d diffIDs", len(cfg.RootFS.DiffIDs))
	}

	if cfg.RootFS.DiffIDs[0] == layer.Digest {
		t.Error("the diffID is the compressed digest; nothing would verify")
	}

	if want := DigestOf([]byte(body)); string(cfg.RootFS.DiffIDs[0]) != want {
		t.Errorf("diffID is %s, wanted the digest of the uncompressed layer %s",
			cfg.RootFS.DiffIDs[0], want)
	}
}

// And the bytes read back as what went in.
func TestACompressedLayerRoundTrips(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	body := strings.Repeat("round and round. ", 1024)

	err := WriteLayout(dir, Spec{
		Ref:    "app:latest",
		Layers: []LayerSource{func(w io.Writer) error { _, err := io.WriteString(w, body); return err }},
	})
	if err != nil {
		t.Fatal(err)
	}

	m := manifestOf(t, dir)

	raw, err := os.ReadFile(filepath.Join(dir, "blobs", "sha256",
		strings.TrimPrefix(m.Layers[0].Digest.String(), "sha256:")))
	if err != nil {
		t.Fatal(err)
	}

	r, err := DecompressFrom(bytes.NewReader(raw), ocispec.MediaTypeImageLayerGzip)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}

	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != body {
		t.Errorf("read back %d bytes, wrote %d", len(got), len(body))
	}
}

// Two writes of one layer give one image, which is what an image's identity
// rests on: a compressor that stamped a time or varied its output would make a
// build produce a different image every run.
func TestCompressionIsDeterministic(t *testing.T) {
	t.Parallel()

	digests := make([]string, 2)

	for i := range digests {
		dir := t.TempDir()

		err := WriteLayout(dir, Spec{
			Ref: "app:latest",
			Layers: []LayerSource{func(w io.Writer) error {
				_, err := io.WriteString(w, strings.Repeat("same every time. ", 512))

				return err
			}},
		})
		if err != nil {
			t.Fatal(err)
		}

		digests[i] = manifestOf(t, dir).Layers[0].Digest.String()
	}

	if digests[0] != digests[1] {
		t.Errorf("two writes gave %s and %s", digests[0], digests[1])
	}
}

func manifestOf(t *testing.T, dir string) ocispec.Manifest {
	t.Helper()

	var index ocispec.Index

	readJSON(t, filepath.Join(dir, "index.json"), &index)

	var m ocispec.Manifest

	readJSON(t, blobAt(dir, index.Manifests[0].Digest.String()), &m)

	return m
}

func configOf(t *testing.T, dir, digest string) ocispec.Image {
	t.Helper()

	var cfg ocispec.Image

	readJSON(t, blobAt(dir, digest), &cfg)

	return cfg
}

func blobAt(dir, digest string) string {
	return filepath.Join(dir, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:"))
}

func readJSON(t *testing.T, at string, into any) {
	t.Helper()

	raw, err := os.ReadFile(at)
	if err != nil {
		t.Fatal(err)
	}

	err = json.Unmarshal(raw, into)
	if err != nil {
		t.Fatalf("read %s: %v", at, err)
	}
}
