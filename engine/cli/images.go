package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/containerd/platforms"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// specFor turns what an Earthfile declared into what the writer needs.
//
// Kept apart from the writing so the mapping can be checked without a layer
// store, and because the two are different kinds of work: this one is about
// what `SAVE IMAGE` meant, and the other about what the format requires.
func specFor(img interp.Image, platform string, layers []string) image.Spec {
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
func writeImages(o Options, e *exec.Executor, stacks func(*ir.Node) []ir.NodeID, images []interp.Image) error {
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

		layers := make([]string, 0, len(stack))
		for _, id := range stack {
			layers = append(layers, filepath.Join(store, "layers", id.String()))
		}

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
