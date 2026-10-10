package fleet

import (
	"context"
	"fmt"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Peers is where a step faults in from: whoever the driver most recently said
// holds this build's layers.
//
// **The gap between the fleet and the sandbox.** A step that reads a file its
// worker did not fetch has to get it from somewhere, and the somewhere is a
// per-assignment list only `Runner` sees. The executor must not know what a
// fleet is, so the list arrives through a value both of them hold: the worker
// makes one, hands it to `Runner` and to its filler, and every assignment
// refreshes it.
//
// Before this, `earth-worker` built that list once at start-up from the driver's
// **control** identity, and `PeerSource` speaks the blob protocol - which that
// endpoint does not offer. Priming and fault-in have never worked between
// machines: the fault E314 found in the probe, in the binary people would run
// (E329).
//
// Empty until something arrives, and it says so rather than guessing. A sink
// that answered before it had been filled would be exactly the start-up-time
// source this replaces.
type Peers struct {
	mu   sync.RWMutex
	from []Fragmenter
}

// Set replaces who this step can fault in from.
func (p *Peers) Set(from []Fragmenter) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.from = from
}

// Fragment asks each peer in turn, nearest first.
//
// The same order `Provision` uses, for the same reason (C.4): the machine that
// produced a layer is the closest copy of it, and asking the driver first makes
// its uplink the whole fleet's bandwidth.
func (p *Peers) Fragment(
	ctx context.Context, id ir.NodeID, want []string, proof bool,
) (manifest, packed []byte, err error) {
	p.mu.RLock()
	from := p.from
	p.mu.RUnlock()

	last := fmt.Errorf("%w: nobody is known to hold %v", ErrNotFetched, id)

	for _, f := range from {
		manifest, packed, err = f.Fragment(ctx, id, want, proof)
		if err == nil {
			return manifest, packed, nil
		}

		last = err
	}

	return nil, nil, last
}
