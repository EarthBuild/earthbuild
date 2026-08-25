package guest

import (
	"context"
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// StoreHas asks the guest which of these layers its store holds.
//
// Asked of the guest rather than answered by a stat on the host, because the
// store is the guest's: under a shared mount both sides could see it and the
// host took the cheaper route, but a store on a disk the guest owns has exactly
// one reader that can answer (E541).
//
// The whole set in one request. The scheduler's question is about a stack, and
// the round trip - not the lookup - is what this costs.
func (c *Client) StoreHas(ctx context.Context, ids []ir.NodeID) ([]ir.NodeID, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	stack := make([]string, len(ids))
	for i, id := range ids {
		stack[i] = id.String()
	}

	resp, err := c.do(ctx, Request{Kind: KindStoreHas, Stack: stack})
	if err != nil {
		return nil, err
	}

	held, err := decodeStack(resp.Held)
	if err != nil {
		return nil, err
	}

	// The reply is data, not truth (green paper §5.3, A5). A peer that names a
	// layer nobody asked about is answering a different question, and the
	// caller's use of this is "the cache's claim is backed by something the
	// store holds" - so an id it did not ask about could confirm a claim
	// against a layer that was never checked.
	asked := make(map[ir.NodeID]bool, len(ids))
	for _, id := range ids {
		asked[id] = true
	}

	for _, id := range held {
		if !asked[id] {
			return nil, fmt.Errorf("the store reported holding %s, which was not"+
				" among the %d layers it was asked about", id, len(ids))
		}
	}

	return held, nil
}

// Squash asks the guest to merge a range of the stack into one layer.
//
// Done where the store is, because a squash reads every layer in the range and
// writes a new one - the largest thing this engine does to a store, and the
// last thing that could sensibly be done from outside it (E557).
//
// The identity is the caller's: Φ derives it from the range, so the guest is
// told what the result is called rather than being asked to decide, and a
// second machine flattening the same range agrees without being consulted.
func (c *Client) Squash(ctx context.Context, into ir.NodeID, rng []ir.NodeID) error {
	stack := make([]string, len(rng))
	for i, id := range rng {
		stack[i] = id.String()
	}

	_, err := c.do(ctx, Request{Kind: KindSquash, Into: into.String(), Stack: stack})

	return err
}

// PackImage asks the guest to write a loadable image archive into its store.
//
// The layers are ids: the host and the guest see the store at different paths,
// so a path from the wrong side names nothing there. Everything else about the
// image is the build's and travels with the request (E558).
func (c *Client) PackImage(
	ctx context.Context, into ir.NodeID, layers []ir.NodeID, spec image.Spec,
) error {
	stack := make([]string, len(layers))
	for i, id := range layers {
		stack[i] = id.String()
	}

	// The layers do not travel: they are functions, and what crosses is the
	// description a build has plus the ids the guest resolves for itself.
	sent := ImageSpecOf(spec)

	_, err := c.do(ctx, Request{
		Kind: KindPackImage, Into: into.String(), Stack: stack, Image: &sent,
	})

	return err
}

// UnpackLayer asks the guest to unpack a compressed blob into its store.
//
// The blob is named rather than sent: the compressed bytes are one large
// sequential read, which a shared mount is perfectly good at, where the tree is
// fifteen thousand small writes, which it is not. See KindUnpackLayer.
func (c *Client) UnpackLayer(ctx context.Context, blob, media string) (ir.NodeID, error) {
	return c.UnpackLayerWithConfig(ctx, blob, media, nil)
}

// UnpackLayerWithConfig is UnpackLayer, filing an image's configuration beside
// the layer it places.
//
// Nil declares nothing, which is the ordinary case: most layers of most images
// carry no configuration, and only the one a `FROM` stands on does.
func (c *Client) UnpackLayerWithConfig(
	ctx context.Context, blob, media string, config []byte,
) (ir.NodeID, error) {
	resp, err := c.do(ctx, Request{
		Kind: KindUnpackLayer, Blob: blob, Media: media, Config: config,
	})
	if err != nil {
		return ir.NodeID{}, err
	}

	id, err := ir.ParseNodeID(resp.Layer)
	if err != nil {
		return ir.NodeID{}, fmt.Errorf("the guest named the layer %q: %w", resp.Layer, err)
	}

	return id, nil
}
