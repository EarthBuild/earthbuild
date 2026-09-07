package fleet

import (
	"bytes"
	"context"
	"io"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Held is the part of a blob store a peer serves from.
//
// An interface rather than `*blob.Store`, so that `engine/fleet` does not depend
// on the store's package for a two-method need - and so a test can serve from a
// map without building a directory.
type Held interface {
	Has(id ir.NodeID) bool
	Get(id ir.NodeID) ([]byte, error)
}

// StoreSource serves blobs a store already holds.
//
// The sender's half of C.4, and what makes a second engine on this machine a
// real peer rather than a diagram: the fetch path runs in an ordinary build
// instead of only in a test with a fake in it.
//
// **A rotted blob is not served.** `blob.Store.Get` verifies what it reads
// against the name it is filed under, so a peer whose disk has decayed answers
// with nothing rather than with rubbish - and the receiver's own check (E238)
// is the second of two, not the only one. Neither is redundant: this one catches
// an honest peer with a bad disk, the other catches a dishonest one.
type StoreSource struct {
	// Label names this peer in diagnostics.
	Label string
	// Blobs is what it holds.
	Blobs Held
}

// Name is this source's label.
func (s *StoreSource) Name() string {
	if s.Label == "" {
		return "store"
	}

	return s.Label
}

// Fetch streams what this store has of these blobs.
//
// Encoded for verified streaming, which is the sender's obligation: the receiver
// cannot check chunks against a tree nobody sent. Absences are silent, because a
// source not having a blob is what the next source is for.
func (s *StoreSource) Fetch(
	_ context.Context, ids []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	out := make(map[ir.NodeID]io.Reader, len(ids))

	for _, id := range ids {
		if s.Blobs == nil || !s.Blobs.Has(id) {
			continue
		}

		b, err := s.Blobs.Get(id)
		if err != nil {
			// Missing, or corrupt on this peer's own disk. Either way it has
			// nothing to offer for this blob and somebody else may.
			continue
		}

		// Plain bytes. A local store has no journey for anything to go wrong
		// on, and every source hands back the same thing (E264): what was
		// asked for, checked to the extent that getting it here could damage
		// it. Encoding here and decoding immediately afterwards would be work
		// to prove a disk read against itself.
		out[id] = bytes.NewReader(b)
	}

	return out, nil
}
