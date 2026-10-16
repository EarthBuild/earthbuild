package sim

import (
	"context"
	"fmt"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Materialiser is a fake core.Materialiser: it tracks stacks in memory and
// mounts nothing.
//
// Fidelity is deliberately narrow, per the contract in
// docs-internals/plan-native-engine.md: it reproduces what the scheduler
// consumes - that a stack can be prepared, that its identity depends on order,
// that handles are independent and releasable - and reproduces no filesystem
// semantics whatsoever. A green run here is not evidence about overlayfs, and
// is not meant to be.
type Materialiser struct {
	live map[string]int // root -> outstanding handles, for leak detection
	mu   sync.Mutex
}

// Materialise implements core.Materialiser.
func (m *Materialiser) Materialise(ctx context.Context, stack []ir.NodeID) (core.Handle, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}

	// The root is derived from the stack in order, so ⟨a,b⟩ and ⟨b,a⟩ differ -
	// the property the conformance suite checks, and the one a set-based
	// implementation would get wrong.
	h := ir.NewHasher()

	h.Count(len(stack))

	for _, id := range stack {
		h.Fixed(id[:])
	}

	root := "/sim/" + h.Sum().String()[:16]

	m.mu.Lock()
	if m.live == nil {
		m.live = map[string]int{}
	}

	m.live[root]++
	m.mu.Unlock()

	return &handle{m: m, root: root}, nil
}

// Outstanding reports handles not yet released. A scheduler that leaks handles
// leaks mounts on a real implementation, so the fake is where that gets caught
// - cheaply, and long before a mount table fills up.
func (m *Materialiser) Outstanding() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	var n int
	for _, c := range m.live {
		n += c
	}

	return n
}

type handle struct {
	m        *Materialiser
	root     string
	released bool
	mu       sync.Mutex
}

func (h *handle) Root() string { return h.root }

// Delta is the same as Root here: the simulator has no layering, and naming a
// separate directory would imply one.
func (h *handle) Delta() string { return h.root }

func (h *handle) Observations() core.Observation {
	// Empty, never nil: callers must not need a nil check at every use.
	return core.Observation{
		Reads:    map[string]ir.NodeID{},
		Listings: map[string]ir.NodeID{},
	}
}

func (h *handle) Release() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.released {
		return nil // idempotent: cleanup paths run more than once
	}

	h.released = true

	h.m.mu.Lock()
	defer h.m.mu.Unlock()

	if h.m.live[h.root] == 0 {
		return fmt.Errorf("release of an unheld root %q", h.root)
	}

	h.m.live[h.root]--
	if h.m.live[h.root] == 0 {
		delete(h.m.live, h.root)
	}

	return nil
}
