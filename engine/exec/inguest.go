package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// EnvUnpackInGuest has the guest unpack an image's layers rather than the host.
//
// **The host cannot grant what an archive declares.** An unprivileged unpack
// tolerates a refused chown, cannot create a device node, and cannot set an
// attribute in the `security.` namespace - so three mechanisms exist to paper
// over the difference between the layer an image describes and the one that
// lands. Unpacking as root inside the guest removes all three questions.
//
// It is also where the store is going, for a reason that has nothing to do with
// privilege: measured from inside the guest, unpacking one layer into the shared
// store takes 4.67s against 2.18s into the block device it owns, and reading it
// back 6.04s against 1.47s - 0.31ms per file a step opens (E511, E676, E677).
//
// This switch moves the unpack. It does not yet move the store, so with the
// layers still on the shared mount it is *slower* than doing nothing - which is
// the point of separating them: the wiring can be exercised before the move it
// is for.
const EnvUnpackInGuest = "EARTH_UNPACK_IN_GUEST"

// materialiseImageInGuest fetches an image's layers and has the guest unpack
// them.
//
// The division follows what each side has. The host has the network, the
// credentials and the manifest; the guest has a filesystem that can hold what
// the archive says and, shortly, the store itself. So the blobs are fetched
// here and unpacked there.
func (e *Executor) materialiseImageInGuest(
	ctx context.Context, n *ir.Node, platform, imageRoot, root, shared string,
) (core.Result, error) {
	c, err := e.client()
	if err != nil {
		return core.Result{}, fmt.Errorf("FROM %s (%s): %w", n.Op.Args[0], n.Meta.Source, err)
	}

	// **Asked, not stated.** A store on a device the guest owns is not on the
	// host's filesystem, so a host that stats it reads an empty answer and
	// rebuilds everything it already had.
	if ids, ok := imageStackNamed(shared); ok {
		held, herr := c.StoreHas(ctx, ids)
		if herr == nil && len(held) == len(ids) {
			return core.Result{
				Layers: ids, Captured: e.sb.Confines(),
				Declares: declarationRemembered(shared),
			}, nil
		}
	}

	seer, ok := e.sb.(interface{ GuestPath(string) (string, bool) })
	if !ok {
		return core.Result{}, fmt.Errorf("FROM %s (%s): this sandbox cannot say"+
			" where the guest sees a host path, so it cannot be handed a blob",
			n.Op.Args[0], n.Meta.Source)
	}

	// Beside the layers rather than in the image cache: this is where a blob
	// already goes when one is kept, and it is a directory the guest can read.
	blobs := filepath.Join(root, "blobs")

	endFetch := phase("image:fetch", n.Op.Args[0])

	fetched, cfg, err := image.FetchApart(ctx, n.Op.Args[0], blobs, image.Options{
		Platform: platform, Challenges: imageRoot,
	})

	endFetch()

	if err != nil {
		return core.Result{}, fmt.Errorf("FROM %s (%s): %w", n.Op.Args[0], n.Meta.Source, err)
	}

	if len(fetched) == 0 {
		return core.Result{}, fmt.Errorf("%s has no layers", n.Op.Args[0])
	}

	// The configuration belongs to the image and a declaration applies to what
	// comes after it, so it travels with the topmost layer - the one a later
	// step stands on.
	raw, err := json.Marshal(cfg)
	if err != nil {
		raw = nil
	}

	ids := make([]ir.NodeID, len(fetched))
	failed := make([]error, len(fetched))

	endUnpack := phase("image:unpack:guest", n.Op.Args[0])

	var (
		unpacking  sync.WaitGroup
		declaredMu sync.Mutex
		declared   ir.NodeID
	)

	for i, l := range fetched {
		at, visible := seer.GuestPath(filepath.Join(blobs, l.At))
		if !visible {
			return core.Result{}, fmt.Errorf("FROM %s (%s): the guest cannot see"+
				" %s, so it cannot unpack it", n.Op.Args[0], n.Meta.Source, l.At)
		}

		config := raw
		if i != len(fetched)-1 {
			config = nil
		}

		unpacking.Add(1)

		go func(i int, at, media string, config []byte) {
			defer unpacking.Done()

			id, d, uerr := c.UnpackLayerDeclaring(ctx, at, media, config)
			if uerr != nil {
				failed[i] = fmt.Errorf("unpack layer %s of %s: %w",
					fetched[i].Digest, n.Op.Args[0], uerr)

				return
			}

			ids[i] = id

			if d != (ir.NodeID{}) {
				declaredMu.Lock()
				declared = d
				declaredMu.Unlock()
			}
		}(i, at, l.MediaType, config)
	}

	unpacking.Wait()
	endUnpack()

	for _, ferr := range failed {
		if ferr != nil {
			return core.Result{}, ferr
		}
	}

	// **The guest's answer, because only the guest could write it.** A
	// declaration is a stack element (§3.2a), so naming it is not enough - a
	// base built on an element nobody wrote fails at materialise with the store
	// saying it holds neither a layer nor a declaration for it, which is how
	// this was found.
	//
	// `store.DeclarationOf` derives the same identity from the configuration
	// the host already has, and a test pins the two equal. It is the check
	// rather than the source.
	declares := declared

	if want := store.DeclarationOf(cfg); want != declares {
		return core.Result{}, fmt.Errorf(
			"FROM %s (%s): the guest wrote declaration %v and the configuration"+
				" says %v\n  a stack element named one way and looked up the"+
				" other is two elements for one image",
			n.Op.Args[0], n.Meta.Source, declares, want)
	}

	rememberImageStack(shared, ids)
	rememberDeclaration(shared, declares)

	return core.Result{
		Layers: ids, Captured: e.sb.Confines(), Declares: declares,
	}, nil
}

// declarationSuffix names what an image's stack declares, beside the note of
// which layers it is.
//
// Remembered rather than recomputed, because the cheap path does not fetch the
// configuration: it finds the stack already held and returns, and a declaration
// it could not name would silently drop the environment the image asked for.
const declarationSuffix = ".declares"

func rememberDeclaration(shared string, id ir.NodeID) {
	if id == (ir.NodeID{}) {
		_ = os.Remove(shared + declarationSuffix)

		return
	}

	_ = os.WriteFile(shared+declarationSuffix, []byte(id.String()), 0o600)
}

func declarationRemembered(shared string) ir.NodeID {
	b, err := os.ReadFile(shared + declarationSuffix) //nolint:gosec // a path this engine derived
	if err != nil {
		return ir.NodeID{}
	}

	id, err := ir.ParseNodeID(string(b))
	if err != nil {
		return ir.NodeID{}
	}

	return id
}
