package fleet

import (
	"bytes"
	"context"
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// Blobs serves fragments out of compressed layer blobs, without the layers ever
// being unpacked.
//
// **The source a lazy pull needs.** Every other Fragmenter packs from a tree in
// the store, which means the tree has to exist - and E654 measured writing one
// at roughly 78% of a layer's unpack, over 15034 entries a build mostly never
// opens. A gzip member cannot be entered in the middle, so the decompression is
// not avoidable; everything after it is.
//
// `Of` is the mapping a pull records: which stored blob unpacks to which layer,
// and in what format. It is a function rather than a map because the answer
// belongs to whoever did the pulling, and this package has no business knowing
// how they wrote it down.
type Blobs struct {
	// Store is 𝔅. Reading from it verifies, so a blob that has rotted is a miss
	// rather than a fragment nobody can check (green paper I4).
	Store *blob.Store
	// Of names the blob a layer came from, and its media type. Not found is an
	// ordinary answer: this source does not have that layer and the caller asks
	// the next one.
	Of func(id ir.NodeID) (at ir.NodeID, mediaType string, ok bool)
}

// **Stated, because it is the whole point.** `Filler` and `ProvisionFragments`
// take sources by this interface, so a blob that satisfies it needs no other
// wiring to become a place a step's faults can be answered from.
var _ Fragmenter = (*Blobs)(nil)

// Fragment sends part of a layer, and the proof only if it is wanted.
//
// **One decompression, both answers.** A gzip member cannot be entered in the
// middle, so asking for the proof and then the fragment meant reading the whole
// blob twice - 1.287s of a 2.612s lazy materialisation, half of it. The proof is
// built whether or not the caller wants it, because it is what makes this source
// trustworthy and it now costs nothing (E658).
//
// A layer this source does not have yields nothing and no error, which is what
// `ProvisionFragments` reads as "ask somebody else".
func (b *Blobs) Fragment(
	_ context.Context, id ir.NodeID, want []string, proof bool,
) (manifest, packed []byte, err error) {
	if b == nil || b.Store == nil || b.Of == nil {
		return nil, nil, nil
	}

	at, mediaType, ok := b.Of(id)
	if !ok {
		return nil, nil, nil
	}

	compressed, err := b.Store.Get(at)
	if err != nil {
		// I4's degrade-to-miss rule: a blob that is absent or does not match
		// its digest is a source that cannot answer, not a build that fails.
		return nil, nil, nil
	}

	zr, err := image.DecompressFrom(bytes.NewReader(compressed), mediaType)
	if err != nil {
		return nil, nil, fmt.Errorf("read the blob for layer %v: %w", id, err)
	}

	defer zr.Close()

	var buf bytes.Buffer

	manifest, err = layer.FragmentFromTar(zr, &buf, want)
	if err != nil {
		return nil, nil, fmt.Errorf("read the blob for layer %v: %w", id, err)
	}

	// **Checked whether or not the caller wanted it.** A manifest's hash is the
	// layer's name, so a blob whose manifest hashes elsewhere is not the layer
	// that was asked for - whatever the mapping said - and its fragment would
	// put one layer's files into another's base. It costs nothing now: the
	// proof came out of the same pass as the fragment.
	if got := layer.ManifestID(manifest); got != id {
		return nil, nil, fmt.Errorf(
			"the blob recorded for layer %v unpacks to %v"+
				"\n  a fragment of it would put files from one layer into another's base,"+
				"\n  so the mapping is wrong and this source will not guess which way",
			id, got)
	}

	if !proof {
		manifest = nil
	}

	return manifest, buf.Bytes(), nil
}
