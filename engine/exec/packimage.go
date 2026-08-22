package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/platforms"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// packImage writes a step's base as an OCI image inside the layer store, and
// tars it where something in the sandbox can load it.
//
// Written here rather than in the guest because the pieces are here: the layer
// directories, the platform and the reference. The guest sees the result
// because the store is the one directory both sides share - the same route
// every layer already takes.
//
// The tar is written by this engine's own packer rather than by the system's
// tar. That is not a preference: a tar produced by macOS carries extended
// attributes, and a Linux daemon reading one refuses it with `lsetxattr
// com.apple.provenance: operation not supported` - a message with nothing in it
// to connect to a build.
func (e *Executor) packImage(_ context.Context, n *ir.Node, base []ir.NodeID) (core.Result, error) {
	if len(n.Op.Args) == 0 {
		return core.Result{}, fmt.Errorf("%s: an image has to be packed under a name", n.Meta.Source)
	}

	if len(base) == 0 {
		return core.Result{}, fmt.Errorf(
			"%s: nothing produced the image %q", n.Meta.Source, n.Op.Args[0])
	}

	root := e.sb.StoreDir()
	st := store.DirStore(root)

	layers := make([]string, 0, len(base))
	for _, id := range base {
		layers = append(layers, st.LayerPath(id))
	}

	spec := image.Spec{Ref: n.Op.Args[0], Layers: layers}

	// What the target declared about how the image runs. Written as layers
	// alone, the loaded image had no entrypoint and no command, and the very
	// next `docker run` answered `no command specified` - from inside a WITH
	// DOCKER block, two targets from the ENTRYPOINT that was dropped.
	// One converter, shared with the path that saves an image to disk. There
	// were two, and the difference between them was `ExposedPorts` and
	// `Volumes` (E44).
	spec.Config = ir.OCIConfig(n.Op.Image)
	spec.Healthcheck = ir.OCIHealthcheck(n.Op.Image)

	// A platform is not optional here, whatever the node says. An image whose
	// config declares no OS or architecture is one docker cannot match against
	// the machine asking for it: the load succeeds, and the very next
	// `docker run` reports the image as not present locally and tries to pull
	// it from a registry that has never heard of it.
	//
	// The node's own platform when it has one, the executor's otherwise - which
	// is the sandbox's, and is where the image is about to be run.
	p, err := parsePlatform(n.Platform)
	if err != nil {
		p, err = parsePlatformString(e.Platform)
	}

	if err != nil {
		p, err = parsePlatformString(DefaultPlatform())
	}

	if err != nil {
		return core.Result{}, fmt.Errorf(
			"%s: cannot tell what platform %s is for", n.Meta.Source, n.Op.Args[0])
	}

	spec.Platform = p

	// Named after this step's own identity, so two loads of different images in
	// one build do not land on each other and a repeat of the same one is the
	// same file.
	dir := filepath.Join(root, "images", n.ID().String())
	err = os.RemoveAll(dir)
	if err != nil {
		return core.Result{}, fmt.Errorf("clear the previous %s: %w", n.Op.Args[0], err)
	}

	err = image.WriteLayout(dir, spec)
	if err != nil {
		return core.Result{}, fmt.Errorf("write %s (%s): %w", n.Op.Args[0], n.Meta.Source, err)
	}

	f, err := os.Create(dir + ".tar") //nolint:gosec // a path this engine derived
	if err != nil {
		return core.Result{}, fmt.Errorf("create the archive for %s: %w", n.Op.Args[0], err)
	}

	defer f.Close()

	_, _, err = image.Pack(dir, f)
	if err != nil {
		return core.Result{}, fmt.Errorf("archive %s: %w", n.Op.Args[0], err)
	}

	// Nothing entered the step's own filesystem: the image went to the store,
	// which is outside it. An empty layer is the honest result, and the step
	// that loads it stands on this one for ordering rather than for content.
	return core.Result{Captured: false}, nil
}

// PackedImagePath is where a packed image waits, as the guest sees it.
//
// Derived from the packing step's identity on both sides rather than passed
// between them, because the host and the guest see the store at different paths
// and a host path handed to the guest names nothing there.
func PackedImagePath(id ir.NodeID) string {
	return guestStore + "/images/" + id.String() + ".tar"
}

// parsePlatformString turns an "os/arch" string into the OCI platform.
func parsePlatformString(s string) (ocispec.Platform, error) {
	p, err := platforms.Parse(s)
	if err != nil {
		return ocispec.Platform{}, err
	}

	return ocispec.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant}, nil
}

// parsePlatform turns the IR's platform into the OCI one.
func parsePlatform(p ir.Platform) (ocispec.Platform, error) {
	if p.OS == "" || p.Arch == "" {
		return ocispec.Platform{}, errors.New("no platform")
	}

	return ocispec.Platform{OS: p.OS, Architecture: p.Arch, Variant: p.Variant}, nil
}
