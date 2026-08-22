package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/ir"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func withConfig(t *testing.T, cfg ocispec.ImageConfig) (store string, layer ir.NodeID) {
	t.Helper()

	store = t.TempDir()
	layer = ir.NodeID{4, 2}

	at := filepath.Join(store, "layers", layer.String())
	if err := os.MkdirAll(at, 0o750); err != nil {
		t.Fatal(err)
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(at+ConfigSuffix, b, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return store, layer
}

// What an image declares becomes a declaration in the store, named by its
// content.
//
// The step that follows then gets it from the stack rather than from a file
// beside the layer - which is what makes it reach a worker at all (§3.2a).
func TestAnImageDeclarationIsWrittenToTheStore(t *testing.T) {
	t.Parallel()

	store, layer := withConfig(t, ocispec.ImageConfig{
		Env:        []string{"PATH=/go/bin:/usr/local/go/bin", "GOPATH=/go"},
		WorkingDir: "/go",
		Cmd:        []string{"/bin/sh"},
	})

	id := declarationFor(store, layer)
	if id == (ir.NodeID{}) {
		t.Fatal("an image that declares an environment produced no declaration")
	}

	d, held, err := decl.Read(store, id)
	if err != nil || !held {
		t.Fatalf("read it back: %v held=%v", err, held)
	}

	if d.WorkingDir != "/go" || !slices.Equal(d.Cmd, []string{"/bin/sh"}) {
		t.Errorf("declaration lost fields: %+v", d)
	}

	// Folded, it puts back exactly what the image said.
	got := decl.Fold(nil, d)
	if !slices.Contains(got, "GOPATH=/go") {
		t.Errorf("folded to %v, want the image's GOPATH", got)
	}
}

// An image's environment is already expanded, and survives the fold unchanged.
//
// A Dockerfile's ENV is resolved when the image is built, so `A=$B` in a config
// means those characters. A declaration stores text *before* expansion (3.10),
// so importing one has to say so or the fold expands it a second time.
func TestAnImageEnvironmentIsNotExpandedAgain(t *testing.T) {
	t.Parallel()

	store, layer := withConfig(t, ocispec.ImageConfig{
		Env: []string{"HOME=/root", "LITERAL=$HOME/x"},
	})

	d, _, err := decl.Read(store, declarationFor(store, layer))
	if err != nil {
		t.Fatal(err)
	}

	got := decl.Fold(nil, d)
	if !slices.Contains(got, "LITERAL=$HOME/x") {
		t.Errorf("an image's literal dollar was expanded: %v", got)
	}
}

// An image that declares nothing adds no element.
//
// "Says nothing" is representable as the absence of a declaration, which is one
// fewer identity on every stack and one fewer thing for a worker to fetch.
func TestAnImageThatDeclaresNothingGetsNoDeclaration(t *testing.T) {
	t.Parallel()

	store, layer := withConfig(t, ocispec.ImageConfig{})

	if id := declarationFor(store, layer); id != (ir.NodeID{}) {
		t.Errorf("an image declaring nothing produced declaration %v", id)
	}
}
