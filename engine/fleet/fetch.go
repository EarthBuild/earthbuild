package fleet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ErrNotFetched marks blobs no source could supply.
var ErrNotFetched = errors.New("some blobs could not be fetched")

// Source is somewhere blobs can be got from.
//
// **Batched, and that is the interface's whole shape.** C.4: "Blobs are
// requested in batches. One stream per blob does not survive a thousand-blob
// synchronisation." A `Fetch(id)` taking one digest would make the batching a
// caller's discipline, which is a discipline somebody eventually skips; taking a
// slice makes a source that opens a thousand streams the source's own mistake
// rather than the protocol's.
type Source interface {
	// Name is for diagnostics: which source served a blob, and which did not.
	Name() string
	// Fetch returns what it has of these, as **verified streams** rather than
	// bytes. A source missing a blob is ordinary - that is what the next source
	// is for - so an absence is not an error.
	//
	// A reader and not a `[]byte`, for two reasons that are the same reason: a
	// layer can be a gigabyte, and C.4 wants a liar caught within a chunk. Both
	// need the bytes to arrive over time rather than all at once, and an
	// interface returning `[]byte` makes that impossible for every
	// implementation at once.
	Fetch(ctx context.Context, ids []ir.NodeID) (map[ir.NodeID]io.Reader, error)
}

// Fetch gets blobs from the best available source, in C.4's order.
//
// Peers holding the blob, then other peers, then the registry. **Multi-source
// fallback is what makes registry availability non-load-bearing** (I6): a build
// whose registry is down still proceeds if any peer has what it needs, which is
// the difference between a shared cache and a single point of failure.
type Fetch struct {
	// Holders are peers announced as having the blob. Asked first because they
	// are the ones that can answer without going further.
	Holders []Source
	// Peers are the rest of the mesh, which may have it anyway.
	Peers []Source
	// Registry is the origin, and is last on purpose.
	Registry Source
}

// Get fetches every id, or says which it could not.
//
// A source that serves **wrong bytes is skipped for those blobs and the next is
// tried**, rather than failing the fetch: a peer serving corruption is a peer
// problem, and I6's whole point is that no single source is load-bearing. The
// digest is checked here rather than trusted, which is what makes an untrusted
// peer safe to fetch from at all (§2.1).
func (f *Fetch) Get(ctx context.Context, ids []ir.NodeID) (map[ir.NodeID][]byte, error) {
	out := make(map[ir.NodeID][]byte, len(ids))

	want := make([]ir.NodeID, 0, len(ids))
	for _, id := range ids {
		want = append(want, id)
	}

	for _, src := range f.order() {
		if len(want) == 0 {
			break
		}

		got, err := src.Fetch(ctx, want)
		if err != nil {
			// A source that cannot answer is not a failure. That is what having
			// several is for.
			continue
		}

		still := want[:0:0]

		for _, id := range want {
			r, ok := got[id]
			if !ok {
				still = append(still, id)

				continue
			}

			// A source hands back plain bytes, having checked they survived
			// the journey (E264). What is left is identity, and for a blob that
			// is the cheapest possible check: a blob is *named by* the digest of
			// its bytes.
			//
			// A layer is not, which is why `Provision` does not come through
			// here - it establishes identity by storing, because for a tree
			// "what is this" and "keep this" are one operation (E263).
			var b bytes.Buffer

			if _, err := io.Copy(&b, r); err != nil {
				still = append(still, id)

				continue
			}

			if BlobID(b.Bytes()) != id {
				// Not what it claimed to be. This source has not supplied it and
				// somebody else may.
				still = append(still, id)

				continue
			}

			out[id] = b.Bytes()
		}

		want = still
	}

	if len(want) > 0 {
		return out, fmt.Errorf("%w: %d of %d, first %v",
			ErrNotFetched, len(want), len(ids), want[0])
	}

	return out, nil
}

// order is C.4's fetch order, holders first and the registry last.
func (f *Fetch) order() []Source {
	out := make([]Source, 0, len(f.Holders)+len(f.Peers)+1)
	out = append(out, f.Holders...)
	out = append(out, f.Peers...)

	if f.Registry != nil {
		out = append(out, f.Registry)
	}

	return out
}

// BlobID is the digest a blob is addressed by.
//
// The same hash the blob store computes when it writes one, streamed over the
// raw bytes. Two different answers to "what is this blob called" would be a
// store that cannot find what the wire fetched.
func BlobID(b []byte) ir.NodeID {
	h := ir.NewHasher()
	h.Fixed(b)

	return h.Sum()
}
