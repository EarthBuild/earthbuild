package exec

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/containerd/platforms"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/fstime"
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
func (e *Executor) packImage(ctx context.Context, n *ir.Node, base []ir.NodeID) (core.Result, error) {
	if len(n.Op.Args) == 0 {
		return core.Result{}, fmt.Errorf("%s: an image has to be packed under a name", n.Meta.Source)
	}

	if len(base) == 0 {
		return core.Result{}, fmt.Errorf(
			"%s: nothing produced the image %q", n.Meta.Source, n.Op.Args[0])
	}

	root := e.sb.StoreDir()

	spec := image.Spec{Ref: n.Op.Args[0]}

	// Only under a clamp. An image that says when it was made is the ordinary
	// thing and what every reader expects, and a time taken from the clock
	// would make two builds of one input differ - so it is written exactly when
	// the build has already said what time to use (E772).
	if at, ok := fstime.Clamp(); ok {
		spec.Created = at
	}

	// What the target declared about how the image runs. Written as layers
	// alone, the loaded image had no entrypoint and no command, and the very
	// next `docker run` answered `no command specified` - from inside a WITH
	// DOCKER block, two targets from the ENTRYPOINT that was dropped.
	// One converter, shared with the path that saves an image to disk. There
	// were two, and the difference between them was `ExposedPorts` and
	// `Volumes` (E44).
	spec.Config = ConfigWithBase(BaseDeclaration(root, base), ir.OCIConfig(n.Op.Image))
	spec.Healthcheck = ir.OCIHealthcheck(n.Op.Image)

	// **The config is a blob a registry serves to anybody who can pull.** The
	// delta scan catches a step that wrote a credential into a file; nothing
	// looked here, and `ENV TOKEN=$SOME_SECRET` puts the value in this
	// structure - where `docker inspect` prints it without being asked.
	//
	// Checked on the host, where the values already are, so nothing new crosses
	// the wire and nothing is checked that a build did not supply.
	// **The exit point.** A layer holding a credential has gone nowhere while it
	// sits in this build's store; saving the image is what sends it somewhere
	// else, so this is where a finding becomes a refusal.
	err := e.refuseLeakedImage(n.Meta.Source, base)
	if err != nil {
		return core.Result{}, err
	}

	{
		found := configSecrets(spec.Config, e.Secrets)
		if len(found) > 0 {
			return core.Result{}, fmt.Errorf(
				"%s: a secret this build was given is in the image's configuration,"+
					" and the image is not written"+
					"\n  %s"+
					"\n  a configuration blob is served to anybody who can pull the image"+
					"\n  pass the value at run time instead of declaring it into the image",
				n.Meta.Source, strings.Join(found, "\n  "))
		}
	}

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

	// **Where the layers are, and where the archive is read.** A packed image
	// is written from layers in the store and loaded by the daemon inside the
	// sandbox, so on a backend with a machine both ends are the guest's and the
	// host was in the middle of its own errand (E558).
	//
	// The guest that is already running, never one started for this: a build
	// reaching a `WITH DOCKER --load` has run steps, so there is one - and if
	// there is not, packing here is what every backend without a machine does.
	c := e.startedClient()
	if c != nil {
		err = c.PackImage(ctx, n.ID(), base, spec)
		if err != nil {
			return core.Result{}, fmt.Errorf("pack %s (%s): %w", n.Op.Args[0], n.Meta.Source, err)
		}

		return core.Result{Captured: false}, nil
	}

	spec.Layers, err = layerSources(root, base)
	if err != nil {
		return core.Result{}, fmt.Errorf("pack %s (%s): %w", n.Op.Args[0], n.Meta.Source, err)
	}

	// Named after this step's own identity, so two loads of different images in
	// one build do not land on each other and a repeat of the same one is the
	// same file.
	err = image.WriteArchive(filepath.Join(root, "images", n.ID().String()), spec)
	if err != nil {
		return core.Result{}, fmt.Errorf("pack %s (%s): %w", n.Op.Args[0], n.Meta.Source, err)
	}

	// Nothing entered the step's own filesystem: the image went to the store,
	// which is outside it. An empty layer is the honest result, and the step
	// that loads it stands on this one for ordering rather than for content.
	return core.Result{Captured: false}, nil
}

// BaseDeclaration is what the stack's own declarations say.
//
// An image's environment travels the stack as a declaration (§3.2a), and until
// now packing read only what the *target* declared - so an image built `FROM
// alpine` was written with no PATH, because alpine's PATH is the base's word
// and not the Earthfile's (E771).
func BaseDeclaration(root string, base []ir.NodeID) decl.Declaration {
	var found []decl.Declaration

	for _, id := range base {
		d, held, err := decl.Read(root, id)
		if err != nil || !held {
			continue
		}

		found = append(found, d)
	}

	// Oldest first, which is the order a stack is in, so a later base overrides
	// an earlier one exactly as it does at run time.
	return decl.Compose(found...)
}

// ConfigWithBase is what the image declares: its base's word, then its own.
//
// Exported because `SAVE IMAGE` writes its layout from engine/cli and packing
// writes one from here, and two paths to one format that disagree about what an
// image says are worse than either being wrong (E773).
//
// **The target wins, and silence is not a word.** A target that sets a variable
// the base also set means to change it, so its value replaces. A target that
// says nothing about the working directory, the user, the entrypoint or the
// command leaves the base's standing - which is what every other engine does
// and what a step already sees at run time. An image is the odd one out only
// because its configuration was assembled at plan time, where the base's
// declaration is not yet known.
func ConfigWithBase(base decl.Declaration, declared ocispec.ImageConfig) ocispec.ImageConfig {
	out := declared

	out.Env = mergedEnv(base.Env, declared.Env)

	if out.WorkingDir == "" {
		out.WorkingDir = base.WorkingDir
	}

	if out.User == "" {
		out.User = base.User
	}

	if len(out.Entrypoint) == 0 {
		out.Entrypoint = base.Entrypoint
	}

	if len(out.Cmd) == 0 {
		out.Cmd = base.Cmd
	}

	return out
}

// mergedEnv is the base's environment with the target's laid over it.
//
// In place rather than appended, so a variable set by both appears once. Two
// entries for one name is a file whose meaning depends on which end a reader
// starts from, and readers differ.
func mergedEnv(base, over []string) []string {
	out := slices.Clone(base)

	for _, e := range over {
		name, _, ok := strings.Cut(e, "=")
		if !ok {
			out = append(out, e)

			continue
		}

		at := slices.IndexFunc(out, func(had string) bool {
			was, _, _ := strings.Cut(had, "=")

			return was == name
		})

		if at < 0 {
			out = append(out, e)

			continue
		}

		out[at] = e
	}

	return out
}

// layerSources turns a stack into the trees an archive is written from.
//
// **Two kinds of element, and only one of them is a tree.** An image's
// environment travels in the stack so that a worker fetching the stack fetches
// it too, and it is filed as `layers/<id>.decl` - a file. Handed to the archive
// writer as a directory it named nothing, and the build failed some way further
// on with `lstat …: no such file or directory` about an id that was never a
// layer. The guest's packer learned this in E749 and the squasher in E751; this
// is the third consumer and the one that had no check at all (E761).
//
// A layer that is genuinely absent is refused here rather than discovered by
// the writer, for the reason the guest refuses it: an image missing a layer
// loads and is missing files, which the daemon reports as a program that is not
// there. Said here, the message can name the build instead of the store's
// layout.
func layerSources(root string, base []ir.NodeID) ([]image.LayerSource, error) {
	st := store.DirStore(root)
	out := make([]image.LayerSource, 0, len(base))

	for _, id := range base {
		if decl.Has(root, id) {
			continue
		}

		if !st.Has(id) {
			return nil, fmt.Errorf("this store holds no layer %s", id)
		}

		out = append(out, image.FromDir(st.LayerPath(id)))
	}

	return out, nil
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
