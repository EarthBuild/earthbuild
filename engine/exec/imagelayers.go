package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// EnvImageLayers stores an image as one layer per layer rather than one layer
// for the whole image.
//
// **Behind a flag while it earns its place.** A `FROM` currently contributes a
// single stack element, and every cache key in existence was derived from that;
// contributing several changes all of them. The trade is measured: up to 38% of
// an image's unpack, once, against 0.67ms per layer per step for as long as the
// build runs, which breaks even somewhere near eighty steps (E646, E648).
const EnvImageLayers = "EARTH_IMAGE_LAYERS"

// EnvImageStream unpacks each layer as it arrives rather than after it lands.
//
// Separate from EnvImageLayers so the two can be measured apart: streaming only
// pays with the layers kept apart, and having one switch would have made the
// pair impossible to tell from either alone.
const EnvImageStream = "EARTH_IMAGE_STREAM"

// stackSuffix names the file remembering which layers an image unpacked into.
const stackSuffix = ".stack"

// materialiseImageApart places each of an image's layers in the store on its
// own, and reports the stack they make.
//
// The layers are kept apart rather than merged, which is what lets them be
// unpacked at once, and what makes assembling them a mount rather than a copy.
func (e *Executor) materialiseImageApart(
	ctx context.Context, n *ir.Node, platform, imageRoot, root, shared string,
) (core.Result, error) {
	st := store.DirStore(root)

	if ids, ok := imageStackNamed(shared); ok && allPopulated(st, ids) {
		return core.Result{
			Layers: ids, Captured: e.sb.Confines(),
			Declares: st.Declaration(ids[len(ids)-1]),
		}, nil
	}

	// **Staged inside the store, not beside the image cache.** `Place` moves a
	// finished tree into the store, and a move within one directory is a
	// rename where a move across one is a copy: staging in the image cache made
	// placing the 64MB layer of `golang:1.26-alpine` cost 0.898s against 0.31s
	// for the whole image merged, which was the entire regression against the
	// merged form.
	apart, err := st.Staging(".apart-")
	if err != nil {
		return core.Result{}, fmt.Errorf("stage %s: %w", n.Op.Args[0], err)
	}

	defer func() { _ = os.RemoveAll(apart) }()

	endFetch := phase("image:fetch", n.Op.Args[0])

	// **Kept only when asked**, because a blob is 61MB of disk per layer that
	// nothing yet reads: `EnvKeepBlobs` is the switch, and E659 records the
	// design question its use turns on.
	keep := newBlobKeeper(root, apart)

	pulled, cfg, err := image.PullApart(ctx, n.Op.Args[0], apart, image.Options{
		Platform: platform, Challenges: imageRoot,
		Stream: os.Getenv(EnvImageStream) != "",
		Retain: keep.retain,
	})

	endFetch()

	if err != nil {
		return core.Result{}, fmt.Errorf("FROM %s (%s): %w", n.Op.Args[0], n.Meta.Source, err)
	}

	endPlace := phase("image:place", n.Op.Args[0])

	// **Placed at once**, because each layer goes to a different address in the
	// store and nothing about placing one depends on another. Serially this was
	// the whole of the regression against the merged form: five layers cost
	// 0.913s where one merged tree cost 0.31s, and that difference was larger
	// than everything keeping the layers apart had saved.
	ids := make([]ir.NodeID, len(pulled))
	failedPlace := make([]error, len(pulled))

	var placing sync.WaitGroup

	for i, l := range pulled {
		placing.Add(1)

		go func(i int, l image.PulledLayer) {
			defer placing.Done()

			endOne := phase("image:place:one", l.Dir)
			id, placeErr := st.PlaceAs(filepath.Join(apart, l.Dir), store.Placement{
				Digests: knownIDs(l.Digests), Owners: declaredBy(l.Owners),
			})

			endOne()

			if placeErr != nil {
				failedPlace[i] = fmt.Errorf("place layer %s of %s: %w",
					l.Digest, n.Op.Args[0], placeErr)

				return
			}

			// **The unpacker already knows, so nothing walks the layer.**
			// A materialiser handed a layer it has no note about scans the
			// whole tree for deletion markers - 1.44s of a cold `golang:1.26-alpine` pull
			// with the layers apart, against zero merged, where the single
			// placed layer is noted here. The pull read every entry to write
			// the layer at all; `Marked` is that answer kept rather than
			// thrown away.
			if !l.Marked {
				st.NoteUnmarked(id)
			}

			// **The join, and the only place both names are in hand.** A blob
			// is named by the hash of its compressed bytes and a layer by the
			// hash of the tree it unpacks to; nothing relates them except the
			// pull that saw both.
			keep.file(st, id, l)

			ids[i] = id
		}(i, l)
	}

	placing.Wait()
	endPlace()

	for _, e := range failedPlace {
		if e != nil {
			return core.Result{}, e
		}
	}

	if len(ids) == 0 {
		return core.Result{}, fmt.Errorf("%s has no layers", n.Op.Args[0])
	}

	// The configuration belongs to the image, and a declaration applies to what
	// comes after it - so it attaches to the topmost layer, which is what a
	// later step stands on.
	top := ids[len(ids)-1]

	written, err := writeConfigBeside(apart, cfg)
	if err == nil {
		_ = st.AdoptConfig(top, written)
		_ = os.Remove(written)
	}
	rememberImageStack(shared, ids)

	return core.Result{
		Layers: ids, Captured: e.sb.Confines(),
		Declares: st.Declaration(ids[len(ids)-1]),
	}, nil
}

// allPopulated reports whether every layer of a remembered stack is still there.
func allPopulated(st store.DirStore, ids []ir.NodeID) bool {
	if len(ids) == 0 {
		return false
	}

	for _, id := range ids {
		if !st.Populated(id) {
			return false
		}
	}

	return true
}

// imageStackNamed is the stack an image was last unpacked into, if it was.
func imageStackNamed(shared string) ([]ir.NodeID, bool) {
	b, err := os.ReadFile(shared + stackSuffix) //nolint:gosec // a path this engine derived
	if err != nil {
		return nil, false
	}

	var out []ir.NodeID

	for line := range strings.FieldsSeq(string(b)) {
		id, perr := ir.ParseNodeID(line)
		if perr != nil {
			return nil, false
		}

		out = append(out, id)
	}

	return out, len(out) > 0
}

// rememberImageStack records the stack, so the next build skips the unpack.
func rememberImageStack(shared string, ids []ir.NodeID) {
	var b strings.Builder

	for _, id := range ids {
		b.WriteString(id.String())
		b.WriteString("\n")
	}

	_ = os.WriteFile(shared+stackSuffix, []byte(b.String()), 0o600)
}

// writeConfigBeside puts an image's configuration where AdoptConfig can take it.
func writeConfigBeside(apart string, cfg ocispec.ImageConfig) (string, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}

	at := apart + store.ConfigSuffix

	err = os.WriteFile(at, b, 0o600)
	if err != nil {
		return "", err
	}

	return at, nil
}

// knownIDs is the unpacker's digests in the engine's type.
//
// A conversion and nothing else: `image.Digest` and `ir.NodeID` are both
// [32]byte over the same function, which
// TestTheUnpackersDigestIsTheEnginesDigest asserts rather than assumes. The two
// cannot share a declaration, because `ir` imports `engine/image`.
func knownIDs(from map[string]image.Digest) map[string]ir.NodeID {
	if len(from) == 0 {
		return nil
	}

	out := make(map[string]ir.NodeID, len(from))
	for path, d := range from {
		out[path] = ir.NodeID(d)
	}

	return out
}

// declaredBy is the archive's account of ownership in the store's type.
//
// A conversion and nothing else, for the reason knownIDs is: `ir` imports
// `engine/image`, so `engine/image` cannot name `layer`.
func declaredBy(from map[string]image.Owner) map[string]layer.Owner {
	if len(from) == 0 {
		return nil
	}

	out := make(map[string]layer.Owner, len(from))
	for at, o := range from {
		out[at] = layer.Owner{UID: o.UID, GID: o.GID}
	}

	return out
}

// EnvKeepBlobs keeps each pulled layer's compressed bytes beside the layer it
// unpacks to.
//
// A blob is 61MB where its tree is 228MB and 15034 files, and a layer kept as a
// blob can still be named (E656) and served in part (E657) at 76% of an
// unpack-and-name (E658). Off by default because nothing reads them yet and
// they are not free: this is the store growing by the compressed size of every
// base image, in exchange for a saving that is not wired up.
const EnvKeepBlobs = "EARTH_KEEP_BLOBS"

// blobKeeper holds each layer's compressed bytes until the layer has a name.
//
// **Two names, learned at different moments.** `Retain` is called with the
// registry's digest, before anything is unpacked; the layer's own id exists only
// after `Place`. So the bytes go to a file named for the registry digest, and
// the join happens later, when both are in hand.
type blobKeeper struct {
	root, at string
	on       bool
	mu       sync.Mutex
	files    map[string]string
}

func newBlobKeeper(root, at string) *blobKeeper {
	return &blobKeeper{
		root:  root,
		at:    at,
		on:    os.Getenv(EnvKeepBlobs) != "",
		files: map[string]string{},
	}
}

// retain is the writer a pull copies a layer's compressed bytes into.
//
// Beside the unpack rather than in the blob store: the store is
// content-addressed and the content is not known until the last byte, so a blob
// written straight into it would need a staging file of its own anyway.
func (k *blobKeeper) retain(digest string) (io.WriteCloser, error) {
	if !k.on {
		return nil, errNotKeeping
	}

	f, err := os.CreateTemp(k.at, ".blob-")
	if err != nil {
		return nil, fmt.Errorf("stage the blob for %s: %w", digest, err)
	}

	k.mu.Lock()
	k.files[digest] = f.Name()
	k.mu.Unlock()

	return f, nil
}

// file moves a retained blob into the blob store and records which layer it is.
//
// Best effort throughout: the layer is unpacked either way, and a blob that
// could not be filed costs the ordinary unpack next time - which is what
// happened before any of this existed.
func (k *blobKeeper) file(st store.DirStore, id ir.NodeID, l image.PulledLayer) {
	if !k.on {
		return
	}

	k.mu.Lock()
	name, ok := k.files[l.Digest]
	k.mu.Unlock()

	if !ok {
		return
	}

	blobs, err := blob.New(filepath.Join(k.root, "blobs"))
	if err != nil {
		return
	}

	f, err := os.Open(name) //nolint:gosec // a temporary file this process made
	if err != nil {
		return
	}

	defer f.Close()

	at, _, err := blobs.Put(f)
	if err != nil {
		return
	}

	st.NoteBlob(id, at, l.MediaType)
}

// errNotKeeping declines a retention without failing a pull. `Retain`'s contract
// is best effort, so the puller carries on with the bytes unkept.
var errNotKeeping = errors.New("not keeping blobs")
