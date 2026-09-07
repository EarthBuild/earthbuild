package guest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/ir"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// The guest packs the archive the host would have packed.
//
// `WITH DOCKER --load` reads a tar the daemon in the sandbox loads, built from
// layers in the store. Both ends of that are the guest's once the store is a
// disk it owns, and the image a build gets must not depend on which side
// assembled it (E558).
func TestTheGuestPacksTheArchiveTheHostWouldHave(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := ir.NodeID{4}
	into := ir.NodeID{5}

	write(t, root, id, map[string]string{"greeting": "hello"})

	spec := image.Spec{
		Ref:      "demo:latest",
		Platform: ocispec.Platform{OS: "linux", Architecture: "arm64"},
		Config:   ocispec.ImageConfig{Entrypoint: []string{"/bin/sh"}},
	}

	c := pairWith(t, &guest.Server{LayerDir: root})

	err := c.PackImage(context.Background(), into, []ir.NodeID{id}, spec)
	if err != nil {
		t.Fatal(err)
	}

	at := filepath.Join(root, "images", into.String())

	// The layout and the archive beside it, which is the pair the loading step
	// relies on.
	for _, want := range []string{at, at + ".tar", filepath.Join(at, "index.json")} {
		_, err = os.Stat(want)
		if err != nil {
			t.Errorf("the guest did not produce %s: %v", filepath.Base(want), err)
		}
	}

	// Byte-for-byte what the host would have written from the same layers, so
	// the archive cannot tell which side packed it.
	host := filepath.Join(t.TempDir(), into.String())

	spec.Layers = []image.LayerSource{image.FromDir(filepath.Join(root, "layers", id.String()))}

	err = image.WriteArchive(host, spec)
	if err != nil {
		t.Fatal(err)
	}

	fromGuest, err := os.ReadFile(at + ".tar")
	if err != nil {
		t.Fatal(err)
	}

	fromHost, err := os.ReadFile(host + ".tar")
	if err != nil {
		t.Fatal(err)
	}

	if len(fromGuest) != len(fromHost) {
		t.Errorf("the guest's archive is %d bytes and the host's is %d",
			len(fromGuest), len(fromHost))
	}
}

// An image naming a layer the store has not got is refused, not half-written.
//
// A missing layer would produce an image that loads and is missing files, which
// the daemon reports as a program that is not there - a message with nothing in
// it to connect to the build that lost the layer.
func TestPackingAnImageWithAMissingLayerIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	c := pairWith(t, &guest.Server{LayerDir: root})

	err := c.PackImage(context.Background(), ir.NodeID{6}, []ir.NodeID{{9}},
		image.Spec{Ref: "demo:latest"})
	if err == nil {
		t.Fatal("an image naming a layer the store does not hold was packed")
	}

	_, statErr := os.Stat(filepath.Join(root, "images"))
	if statErr == nil {
		t.Error("a refused pack left an images directory behind")
	}
}

// A declaration in the stack is not a missing layer.
//
// An image's environment travels as a stack element rather than a file beside
// the layer, so that a worker fetching every id in the stack fetches it too
// (green paper §3.2a). It is written as `layers/<id>.decl`, a file - so the
// layer test, which stats `layers/<id>` and wants a directory, can never be
// satisfied by one. Packing asked that test of every element and refused a
// correct build: `WITH DOCKER --load` of an image whose base declares anything
// reported the declaration as a layer the store had lost (E749).
func TestPackingAnImageWhoseStackCarriesADeclaration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	layer := ir.NodeID{4}
	into := ir.NodeID{5}

	write(t, root, layer, map[string]string{"greeting": "hello"})

	declares, err := decl.Write(root, decl.Declaration{
		Env: []string{"PATH=/usr/bin"}, WorkingDir: "/w",
	})
	if err != nil {
		t.Fatal(err)
	}

	c := pairWith(t, &guest.Server{LayerDir: root})

	// Above the layer it came with, which is where the scheduler puts it.
	err = c.PackImage(context.Background(), into, []ir.NodeID{layer, declares},
		image.Spec{Ref: "demo:latest"})
	if err != nil {
		t.Fatalf("a stack carrying a declaration was refused: %v", err)
	}

	// The declaration is not a filesystem layer, so the archive holds one.
	manifest, err := os.ReadFile(filepath.Join(root, "images", into.String(), "index.json"))
	if err != nil {
		t.Fatal(err)
	}

	if len(manifest) == 0 {
		t.Error("the pack wrote an empty index")
	}
}
