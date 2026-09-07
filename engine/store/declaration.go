package store

import (
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/ir"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// declarationFor turns what an image declared into a stack element, returning
// its identity.
//
// **A declaration is an element, not a file beside the layer** (green paper
// §3.2a). Written here, where the image has just been placed and its
// configuration is on disk, so the step that follows finds it on the stack -
// which is how it reaches a worker, since a worker fetches every id in a stack
// and nothing ever fetched a sidecar.
//
// Zero when the image declares nothing, which is the honest encoding of "says
// nothing": one fewer identity on every stack, and one fewer thing to fetch.
//
// Best effort. A declaration that cannot be written costs the environment an
// image asked for, which is a build that behaves as it did before this existed;
// failing the FROM instead would turn a degraded build into no build.
func declarationFor(store string, layer ir.NodeID) ir.NodeID {
	cfg, err := ReadImageConfig(filepath.Join(store, "layers", layer.String()) + ConfigSuffix)
	if err != nil {
		return ir.NodeID{}
	}

	d, ok := declarationFrom(cfg)
	if !ok {
		return ir.NodeID{}
	}

	id, err := decl.Write(store, d)
	if err != nil {
		return ir.NodeID{}
	}

	return id
}

// DeclarationOf is the identity of what a configuration declares, without a
// store to read it from or write it to.
//
// **A host cannot read a sidecar on a device it does not have.** The store is
// moving onto the block device the guest owns, and `declarationFor` answers by
// reading a file beside the layer - which is fine while both sides see one
// directory and is exactly the assumption that move removes.
//
// Nothing needs reading: the configuration was fetched over the network a moment
// before the layer was placed, so the caller has it. What must not differ is the
// answer, because a stack element derived one way and looked up the other would
// be two elements for one image (§3.2a) - so both go through
// `declarationFrom`, and a test compares them over a spread of configurations.
//
// This does not *write* the declaration. Writing is what puts it where a worker
// can fetch it, and that belongs wherever the store is.
func DeclarationOf(cfg ocispec.ImageConfig) ir.NodeID {
	d, ok := declarationFrom(cfg)
	if !ok {
		return ir.NodeID{}
	}

	return decl.ID(d)
}

// declarationFrom converts an image's configuration, reporting whether it says
// anything at all.
//
// **Compared by identity, not field by field.** 𝒮(γ) covers every field and a
// test enforces that, so "declares nothing" is "hashes as the empty declaration
// does" - and stays right when a field is added, which a hand written emptiness
// check would not.
func declarationFrom(cfg ocispec.ImageConfig) (decl.Declaration, bool) {
	// **Literal, because an image's environment is already expanded.** A
	// Dockerfile's ENV is resolved when the image is built, so `A=$B` in a
	// configuration means those characters; a declaration stores text before
	// expansion (3.10), so importing one without saying so expands it twice.
	d := decl.Literal(cfg.Env)
	d.WorkingDir = cfg.WorkingDir
	d.User = cfg.User
	d.Entrypoint = cfg.Entrypoint
	d.Cmd = cfg.Cmd

	if decl.ID(d) == decl.ID(decl.Declaration{}) {
		return decl.Declaration{}, false
	}

	return d, true
}
