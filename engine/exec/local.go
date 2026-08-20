package exec

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Local is a sandbox that is not one: the guest runs in this process and steps
// run directly on the host filesystem.
//
// It exists because a developer on a machine with no VM still needs the engine
// to do something, and because it exercises the real guest protocol end to end
// with no machine boundary to obscure a failure.
//
// **It provides no isolation**, so green paper A3 does not hold and ε does not
// bound what a step observed. Everything it produces is therefore uncaptured
// and uncacheable - not as a limitation to be lifted later, but as the correct
// consequence: results from an unconfined step must never enter a cache that a
// confined build would trust.
type Local struct {
	root string
	both []net.Conn
}

// Start creates a working directory and serves a guest over an in-memory pipe.
func (l *Local) Start(ctx context.Context) (Conn, error) {
	root, err := os.MkdirTemp("", "earthbuild-local-")
	if err != nil {
		return nil, fmt.Errorf("create the local work directory: %w", err)
	}

	l.root = root

	host, guestSide := net.Pipe()
	l.both = []net.Conn{host, guestSide}

	srv := &guest.Server{Mat: &hostMat{root: root}, Unconfined: true}
	go func() { _ = srv.Serve(ctx, guestSide) }()

	return &pipeConn{Conn: host, other: guestSide}, nil
}

// StoreDir is the local working directory. Layers are not assembled here - the
// local backend has no overlay - so this exists to satisfy the port rather than
// to be useful.
func (l *Local) StoreDir() string { return l.root }

// Confines is false, and permanently so: this is the local backend's defining
// property, not a gap. Steps run on the host with no namespace, no chroot and
// no cgroup, so nothing it produces may be cached.
func (l *Local) Confines() bool { return false }

// Stop removes the working directory. A local run that leaves its scratch
// behind fills a developer's disk one build at a time.
func (l *Local) Stop() error {
	for _, c := range l.both {
		_ = c.Close()
	}

	if l.root == "" {
		return nil
	}

	err := os.RemoveAll(l.root)
	if err != nil {
		return fmt.Errorf("remove the local work directory: %w", err)
	}

	return nil
}

// hostMat hands out the host filesystem. It satisfies core.Materialiser and
// nothing more: no layering, no overlay, no confinement.
type hostMat struct{ root string }

func (m *hostMat) Materialise(_ context.Context, _ []ir.NodeID) (core.Handle, error) {
	return hostHandle{root: m.root}, nil
}

type hostHandle struct{ root string }

func (h hostHandle) Root() string   { return h.root }
func (h hostHandle) Delta() string  { return h.root }
func (h hostHandle) Release() error { return nil }

// Observations reports nothing, and says so by being empty rather than by
// claiming the step read nothing: Result.Observed stays false, so no Κ₂ entry
// is ever derived from it.
func (h hostHandle) Observations() core.Observation {
	return core.Observation{
		Reads:    map[string]ir.NodeID{},
		Listings: map[string]ir.NodeID{},
	}
}
