package guest

import (
	"context"
	"fmt"

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
