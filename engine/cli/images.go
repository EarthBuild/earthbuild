package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/fstime"
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
func specFor(
	img interp.Image, platform string, layers []image.LayerSource,
	base decl.Declaration, created time.Time,
) image.Spec {
	// The same two conversions the packed-image path uses, and for the reason
	// that path exists: this was a third hand-written copy of the same fields,
	// and it was the one that had them all while the other did not (E44).
	spec := image.Spec{
		Ref:    img.Ref,
		Layers: layers,
		// The base's word and then the target's, as the packed path composes
		// it: an image built FROM alpine declares alpine's PATH whichever way it
		// is written (E771, E773).
		Config: exec.ConfigWithBase(base, ir.OCIConfig(img.Config.ToIR())),
		// The extension the OCI configuration has no field for, carried beside
		// it and through the same converter (E486).
		Healthcheck: ir.OCIHealthcheck(img.Config.ToIR()),
		// Only under a clamp; see image.Spec.Created and E772.
		Created: created,
	}

	// **An image always says what machine it is for.** `architecture` and `os`
	// are required by the image specification, and this parsed the platform,
	// discarded the error, and left the zero value in place - so a build with no
	// `--platform`, which is nearly every build, wrote both empty. `docker
	// inspect` reported no architecture on an image that was otherwise right,
	// and a registry that validates its input would refuse it (E793).
	//
	// The default stands in for an answer that was never given; it does not
	// replace one that was. A platform that will not parse falls back here too,
	// because an image nothing can place is not the better failure - but the
	// string reaching this function unparsed is a separate defect, and this is
	// deliberately not the place that hides it.
	p, err := platforms.Parse(platform)
	if err != nil {
		p = platforms.DefaultSpec()
	}

	spec.Platform = ocispec.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant}

	return spec
}

// emptyStackIsExpected reports whether a node having no layers is the answer
// rather than the absence of one.
//
// **A node that never ran and a node that ran and made nothing look identical
// from here**: both have an empty stack. Only the operation tells them apart,
// and exactly one of them means nothing - `FROM scratch` *is* the empty image,
// which is how a from-nothing base is made, and `tests/scratch-test.earth` is
// that file in full.
//
// Deliberately not a general "no layers is fine": a RUN that produced none did
// not run, and saying so is the whole value of the check.
func emptyStackIsExpected(n *ir.Node) bool {
	return n != nil && n.Op.Kind == ir.OpScratch
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
	stacks func(*ir.Node) []ir.NodeID, declared func(ir.NodeID) bool,
	images []interp.Image, inGraph map[ir.NodeID]bool,
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

		// The same reading that applies to AS LOCAL: an interpretation the graph
		// never reached has no layers to send - see scheduled().
		if !inGraph[img.From.ID()] {
			continue
		}

		stack := stacks(img.From)
		if len(stack) == 0 && !emptyStackIsExpected(img.From) {
			return fmt.Errorf("SAVE IMAGE %s (%s): the step producing it did not run", img.Ref, img.Source)
		}

		// **The other exit point.** A layer holding a credential has gone
		// nowhere while it sits in this build's store; writing the image is
		// what sends it somewhere else, and this is the path an ordinary `SAVE
		// IMAGE` takes. The packed-image path in engine/exec has checked since
		// the mechanism was written and this one never did, so the detection
		// ran, wrote its note beside the layer, and the image was published
		// with the secret in it regardless.
		err = e.RefuseLeakedImage(img.Source, stack)
		if err != nil {
			return err
		}

		layers := layerSources(ctx, e, store, stack, declared)

		// Named after the reference so two images from one build do not land on
		// each other, and sanitised because a reference holds slashes and colons
		// that a directory name cannot.
		dir := filepath.Join(root, "images", refDir(img.Ref))
		err := os.RemoveAll(dir)
		if err != nil {
			return fmt.Errorf("clear the previous %s: %w", img.Ref, err)
		}

		created, _ := fstime.Clamp()

		err = image.WriteLayout(dir, specFor(img, o.Platform, layers,
			exec.BaseDeclaration(store, stack), created))
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
	ctx context.Context, e *exec.Executor, storeRoot string,
	stack []ir.NodeID, declared func(ir.NodeID) bool,
) []image.LayerSource {
	packer, ok := e.Sandbox().(interface {
		PackLayer(context.Context, ir.NodeID, io.Writer) error
	})

	// **The guest's store is not the host's to look in.** A sandbox that packs
	// its own layers keeps them on a device this process cannot open, so every
	// question about what is *there* has to be answered without looking - see
	// treeSources for the one that matters.
	if ok {
		return treeSources(ctx, stack, declared, packer.PackLayer)
	}

	layerstore := store.LayerStore(storeRoot)
	out := make([]image.LayerSource, 0, len(stack))

	for _, id := range stack {
		// The store is this process's own here, so it can be asked as well as
		// told: an element it holds neither way is one nothing can pack, and
		// skipping it is what this always did.
		if declared(id) || !layerstore.Has(id) {
			continue
		}

		out = append(out, image.FromDir(layerstore.Path(id)))
	}

	return out
}

// treeSources is the packable half of a stack.
//
// **A stack holds declarations as well as trees** (green paper §3.2a), and only
// the trees are layers. This used to tell them apart by asking the host's layer
// store which elements it held, which is true only while the host and the
// sandbox share one directory - and stopped being true when the microVM became
// the default on Linux. The store moved onto a device the host cannot open, the
// answer became "not here" for every element, and `SAVE IMAGE` wrote images with
// no filesystem in them at all: 2 layers under the namespace backend, 0 under
// the microVM, and `docker run` on the result unable to find `ls`.
//
// So the question is put to the party that knows it without looking anywhere:
// the scheduler sees `Declares` on every result it finishes, run or cached, and
// a declaration is a declaration wherever its bytes happen to live.
func treeSources(
	ctx context.Context,
	stack []ir.NodeID,
	declared func(ir.NodeID) bool,
	pack func(context.Context, ir.NodeID, io.Writer) error,
) []image.LayerSource {
	out := make([]image.LayerSource, 0, len(stack))

	for _, id := range stack {
		if declared(id) {
			continue
		}

		out = append(out, func(w io.Writer) error { return pack(ctx, id, w) })
	}

	return out
}
