package image

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Spec is an image to write: what it is called, what it is made of, and how it
// starts.
type Spec struct {
	// Ref is what the image is called - `app:latest`. It becomes the layout's
	// ref name, which is what `docker load` and `skopeo copy` read to know what
	// they have been handed.
	Ref string
	// Platform is what the image was built for. In the config because a runtime
	// checks it before starting anything.
	Platform ocispec.Platform
	// Layers are directories, oldest first, in the order they stack.
	Layers []string
	// Config is what SAVE IMAGE declared: entrypoint, environment, labels.
	Config ocispec.ImageConfig
	// Healthcheck is how a running container reports its own health, nil when
	// the image declares none.
	//
	// Beside `Config` rather than in it, because `ocispec.ImageConfig` has no
	// field for one: a health check is Docker's extension to the image
	// configuration and not OCI's. An image config is a JSON object that both
	// read, so it is written alongside the standard fields - which is what every
	// other builder does and what a daemon looks for (E486).
	Healthcheck *Healthcheck
}

// Healthcheck is a health check as an image config carries it.
//
// Durations are nanoseconds on the wire, which is how Docker's own config
// writes them.
type Healthcheck struct {
	Test          []string      `json:"Test,omitempty"`
	Interval      time.Duration `json:"Interval,omitempty"`
	Timeout       time.Duration `json:"Timeout,omitempty"`
	StartPeriod   time.Duration `json:"StartPeriod,omitempty"`
	StartInterval time.Duration `json:"StartInterval,omitempty"`
	Retries       int           `json:"Retries,omitempty"`
}

// configWith is an image configuration plus the extension OCI does not define.
//
// A struct rather than a map, and embedded rather than copied field by field:
// the standard fields keep their own marshalling, and the extension is one more
// key beside them. Copying them by hand here would be the third such copy, and
// the second one disagreed (E44).
type configWith struct {
	ocispec.Image

	Config configBody `json:"config,omitempty"`
}

// configBody is `config` with the extension in it.
type configBody struct {
	ocispec.ImageConfig

	Healthcheck *Healthcheck `json:"Healthcheck,omitempty"`
}

// WriteLayout writes an image as an OCI layout.
//
// The layout is the interchange format rather than one of several options:
// `docker load`, `skopeo copy`, `crane push` and every registry client start
// here. Writing something almost-but-not-quite like it would produce a
// directory only this engine understands, which is the opposite of the point -
// so the types come from image-spec and the structure is whatever that says.
//
// Layers are uncompressed. That keeps a layer's digest and its diff id the same
// value, which removes a whole class of mismatch, and it avoids gzip - whose
// header carries a modification time, so compressing would put a clock back
// into an image built to be reproducible.
func WriteLayout(dir string, spec Spec) error {
	if spec.Ref == "" {
		return errors.New("an image needs a name")
	}

	blobs := filepath.Join(dir, "blobs", "sha256")
	err := os.MkdirAll(blobs, 0o750)
	if err != nil {
		return fmt.Errorf("prepare the layout at %s: %w", dir, err)
	}

	layers, diffIDs, err := writeLayers(blobs, spec.Layers)
	if err != nil {
		return err
	}

	configDesc, err := writeConfig(blobs, spec, diffIDs)
	if err != nil {
		return err
	}

	manifestDesc, err := writeBlob(blobs, ocispec.MediaTypeImageManifest, ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    layers,
	})
	if err != nil {
		return fmt.Errorf("write the manifest: %w", err)
	}

	// The ref name is how a tool knows what to call what it has been given.
	// Without it `docker load` reports an image with no tag, which is loaded and
	// then unfindable.
	// Two annotations, because two things read this and they read different
	// ones. `org.opencontainers.image.ref.name` is the OCI convention and what
	// skopeo and crane look for; containerd's image store - which is what
	// docker uses when it can load an OCI layout at all - keys on
	// `io.containerd.image.name`, and wants the reference in full.
	//
	// With only the first, a loaded image was listed by `docker images` twice
	// and denied by `docker image inspect`, and `docker run` went looking for
	// it in a registry. It ran perfectly well by ID, which is what showed the
	// image was right and only its name was wrong.
	manifestDesc.Annotations = map[string]string{
		ocispec.AnnotationRefName:  spec.Ref,
		"io.containerd.image.name": FullReference(spec.Ref),
	}
	manifestDesc.Platform = &spec.Platform

	err = writeJSON(filepath.Join(dir, "index.json"), ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{manifestDesc},
	})
	if err != nil {
		return fmt.Errorf("write the index: %w", err)
	}

	err = writeJSON(filepath.Join(dir, "oci-layout"), ocispec.ImageLayout{
		Version: ocispec.ImageLayoutVersion,
	})
	if err != nil {
		return fmt.Errorf("write the layout marker: %w", err)
	}

	return nil
}

// writeLayers packs each directory into the blob store.
//
// The digest and the diff id are the same value here, because the layers are
// not compressed: a diff id names the uncompressed bytes and a layer descriptor
// names what is stored, and storing them uncompressed makes those the same
// thing.
func writeLayers(blobs string, dirs []string) ([]ocispec.Descriptor, []digest.Digest, error) {
	var (
		descs   []ocispec.Descriptor
		diffIDs []digest.Digest
	)

	for _, d := range dirs {
		// Written to a temporary name and moved, because a blob's name is the
		// digest of its contents and that is not known until it has been
		// written. A partially written blob under its final name is a cache
		// entry that claims to be something it is not.
		tmp, err := os.CreateTemp(blobs, ".packing-*")
		if err != nil {
			return nil, nil, fmt.Errorf("stage a layer: %w", err)
		}

		dgst, size, err := Pack(d, tmp)
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())

			return nil, nil, err
		}

		err = tmp.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("finish a layer: %w", err)
		}

		err = os.Rename(tmp.Name(), filepath.Join(blobs, strings.TrimPrefix(dgst, "sha256:")))
		if err != nil {
			return nil, nil, fmt.Errorf("store a layer: %w", err)
		}

		descs = append(descs, ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageLayer,
			Digest:    digest.Digest(dgst),
			Size:      size,
		})
		diffIDs = append(diffIDs, digest.Digest(dgst))
	}

	return descs, diffIDs, nil
}

// writeConfig writes the image configuration and returns its descriptor.
func writeConfig(blobs string, spec Spec, diffIDs []digest.Digest) (ocispec.Descriptor, error) {
	// No `created` timestamp. It is the one field the format invites that would
	// make two builds of one input produce different images, which is the
	// property this engine is for.
	cfg := configWith{
		Image: ocispec.Image{
			Platform: spec.Platform,
			RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: diffIDs},
		},
		Config: configBody{
			ImageConfig: spec.Config,
			Healthcheck: spec.Healthcheck,
		},
	}

	desc, err := writeBlob(blobs, ocispec.MediaTypeImageConfig, cfg)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("write the config: %w", err)
	}

	return desc, nil
}

// writeBlob stores a JSON document under its own digest.
func writeBlob(blobs, mediaType string, v any) (ocispec.Descriptor, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("encode: %w", err)
	}

	dgst := DigestOf(b)

	path := filepath.Join(blobs, strings.TrimPrefix(dgst, "sha256:"))
	err = os.WriteFile(path, b, 0o600)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("write %s: %w", path, err)
	}

	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.Digest(dgst),
		Size:      int64(len(b)),
	}, nil
}

// writeJSON writes a document at a fixed name.
func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}

	err = os.WriteFile(path, b, 0o600)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}
