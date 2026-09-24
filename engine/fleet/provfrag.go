package fleet

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Fragmenter is a source that can send part of a layer with its proof.
//
// Separate from `Source` because the answers are different shapes: a blob is
// bytes whose digest the caller already knows, and a fragment is bytes plus the
// manifest that authenticates them against a layer whose digest says nothing
// about any subset (E284).
type Fragmenter interface {
	// Fragment sends part of a layer, and the proof only if it is wanted.
	//
	// **A manifest crosses once per layer, not once per fragment.** It is about
	// a hundred bytes an entry against a fragment of only what was read, so for
	// the case this exists for - a small read set from a large base - the proof
	// is the dominant cost (E298). A caller that already has it says so, and one
	// flag on the request is the whole mechanism (E299).
	Fragment(
		ctx context.Context, id ir.NodeID, want []string, proof bool,
	) (manifest, packed []byte, err error)
}

// ProvisionFragments fetches the part of each input a step was predicted to
// read.
//
// The same shape as `Provision`: sources in order, skip what is here, store as
// you go - with the layer replaced by the part of it somebody asked for (E288).
// Storing *is* verifying here as it is there, because `Fragments.PutVerified`
// checks the manifest against the layer's name and then every file against the
// manifest (E285).
//
// **Nothing predicted is nothing asked for.** A worker that has not been told
// what its step reads has to fetch whole layers, and this says so by doing
// nothing rather than by requesting a fragment of nothing.
//
// A source that answers with somebody else's layer is skipped and the next
// tried, exactly as one serving wrong bytes is (I6). The forgery to worry about
// is not rubbish: it is a *coherent* fragment of a different layer with its own
// honest manifest, which every check but the first would pass.
func ProvisionFragments(
	ctx context.Context, into *Fragments, a Assignment, from ...Fragmenter,
) (Transfer, error) {
	want := a.Hints.ReadsPredicted
	if len(want) == 0 || into == nil {
		return Transfer{}, nil
	}

	began := time.Now()
	moved := Transfer{}

	var missing []ir.NodeID

	for _, id := range standsOn(a) {
		if !into.Has(id, want) {
			missing = append(missing, id)
		}
	}

	for _, id := range missing {
		n, err := fetchFragment(ctx, into, id, want, from)
		if err != nil {
			return moved, err
		}

		moved.Bytes += n
	}

	moved.Took = time.Since(began)

	return moved, nil
}

// fetchFragment asks each source in turn until one gives an answer that checks.
func fetchFragment(
	ctx context.Context, into *Fragments, id ir.NodeID, want []string, from []Fragmenter,
) (int64, error) {
	var last error

	// The proof, only if this worker has not got it already.
	manifest, have := into.Manifest(id)

	for _, src := range from {
		got, packed, err := src.Fragment(ctx, id, want, !have)
		if err != nil {
			last = err

			continue
		}

		if !have {
			manifest = got
		}

		err = into.PutVerified(id, want, manifest, bytes.NewReader(packed))
		if err != nil {
			// Not what it claimed to be. Somebody else may have the real thing,
			// and a peer with a rotted disk costs a retry rather than a build.
			last = err

			continue
		}

		n := int64(len(packed))
		if !have {
			n += int64(len(manifest))
		}

		return n, nil
	}

	return 0, fmt.Errorf("no fragment of %v that checks out: %w", id, last)
}

// standsOn is every layer an assignment reads, base first and once each.
//
// Base first because the driver names holders in that order and because it is
// the biggest thing that would otherwise move; once each because a base is
// commonly also a source, and asking twice fetches twice.
func standsOn(a Assignment) []ir.NodeID {
	out := make([]ir.NodeID, 0, len(a.Base))
	seen := make(map[ir.NodeID]bool, len(a.Base))

	add := func(id ir.NodeID) {
		if !seen[id] {
			seen[id] = true

			out = append(out, id)
		}
	}

	for _, id := range a.Base {
		add(id)
	}

	for _, stack := range a.Sources {
		for _, id := range stack {
			add(id)
		}
	}

	return out
}
