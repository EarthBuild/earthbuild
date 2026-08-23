package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
	"github.com/containerd/platforms"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// specFor turns what an Earthfile declared into what the writer needs.
//
// Kept apart from the writing so the mapping can be checked without a layer
// store, and because the two are different kinds of work: this one is about
// what `SAVE IMAGE` meant, and the other about what the format requires.
func specFor(img interp.Image, platform string, layers []image.LayerSource) image.Spec {
	// The same two conversions the packed-image path uses, and for the reason
	// that path exists: this was a third hand-written copy of the same fields,
	// and it was the one that had them all while the other did not (E44).
	spec := image.Spec{
		Ref:    img.Ref,
		Layers: layers,
		Config: ir.OCIConfig(img.Config.ToIR()),
		// The extension the OCI configuration has no field for, carried beside
		// it and through the same converter (E486).
		Healthcheck: ir.OCIHealthcheck(img.Config.ToIR()),
	}

	p, err := platforms.Parse(platform)
	if err == nil {
		spec.Platform = ocispec.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant}
	}

	return spec
}

// writeImages writes every image a build declared, as an OCI layout each.
//
// A layout on disk rather than a load into a running daemon, because that is
// what this engine can honestly do: the layout is the interchange format, and
// `docker load --input` or `skopeo copy oci:...` takes it from here. Saying
// where it went is part of the job - an image written somewhere nobody is told
// about has not really been produced.
func writeImages(
	ctx context.Context, o Options, e *exec.Executor,
	stacks func(*ir.Node) []ir.NodeID, images []interp.Image,
) error {
	if len(images) == 0 {
		return nil
	}

	store := e.Sandbox().StoreDir()

	root, err := storeDir()
	if err != nil {
		return err
	}

	for _, img := range images {
		if img.From == nil {
			return fmt.Errorf("SAVE IMAGE %s (%s): nothing produces it", img.Ref, img.Source)
		}

		stack := stacks(img.From)
		if len(stack) == 0 {
			return fmt.Errorf("SAVE IMAGE %s (%s): the step producing it did not run", img.Ref, img.Source)
		}

		layers := layerSources(ctx, e, store, stack)

		// Named after the reference so two images from one build do not land on
		// each other, and sanitised because a reference holds slashes and colons
		// that a directory name cannot.
		dir := filepath.Join(root, "images", refDir(img.Ref))
		err := os.RemoveAll(dir)
		if err != nil {
			return fmt.Errorf("clear the previous %s: %w", img.Ref, err)
		}

		err = image.WriteLayout(dir, specFor(img, o.Platform, layers))
		if err != nil {
			return fmt.Errorf("write %s (%s): %w", img.Ref, img.Source, err)
		}

		fmt.Fprintf(o.Out, "  %-14s %s -> %s%s\n", img.Source, img.Ref, dir, pushNote(img.Push))
	}

	return nil
}

// refDir turns an image reference into one directory name.
func refDir(ref string) string {
	out := []rune(ref)
	for i, r := range out {
		if r == '/' || r == ':' || r == os.PathSeparator {
			out[i] = '_'
		}
	}

	return string(out)
}

// pushNote says what did not happen to an image declared for publishing.
//
// `SAVE IMAGE --push` is a declaration the *invocation* decides on, which is how
// the tool that ships behaves, and this engine has no flag to decide it with -
// so not pushing is correct. Saying nothing about it is not: someone who wrote
// `--push` and watched a build succeed has been given every reason to think the
// image was published.
func pushNote(push bool) string {
	if !push {
		return ""
	}

	return " (declared --push; not pushed - this engine writes images, it does not publish them)"
}

// layerSources is where this image's layers come from.
//
// **From the guest where the guest is the only one that can open them.** A
// sandbox whose store is a directory this process shares is read here, which is
// every backend today and will stay true of the ones that confine with
// namespaces - their store is local and always will be. A sandbox whose store
// is a disk it owns packs each layer itself and streams it out (E556).
//
// Asked of the sandbox by capability rather than by name, so a backend that
// cannot pack is not a special case here: it simply does not answer, and the
// directory path is what this always did.
func layerSources(
	ctx context.Context, e *exec.Executor, storeRoot string, stack []ir.NodeID,
) []image.LayerSource {
	packer, ok := e.Sandbox().(interface {
		PackLayer(context.Context, ir.NodeID, io.Writer) error
	})

	layerstore := store.LayerStore(storeRoot)

	out := make([]image.LayerSource, 0, len(stack))

	for _, id := range stack {
		// **A stack holds declarations as well as trees** (green paper §3.2a),
		// and only the trees are layers. An image built from every element
		// asked the packer for a `.decl` and was told there was no such layer,
		// which is true and is not the caller's mistake - it is the same split
		// the materialiser makes with `classify`, made here for the same
		// reason (I18).
		if !layerstore.Has(id) {
			continue
		}

		if ok {
			out = append(out, func(w io.Writer) error {
				return packer.PackLayer(ctx, id, w)
			})

			continue
		}

		out = append(out, image.FromDir(layerstore.Path(id)))
	}

	return out
}
