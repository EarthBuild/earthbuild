package guest

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"google.golang.org/grpc"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/remote"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// withActions serves REAPI to a step, for exactly as long as the step runs.
//
// **Beside the step, in the same shape `withDaemon` is.** A step asks for
// something running alongside it that it talks to over a socket in its own
// filesystem; that is what a WITH DOCKER asks for and what a WITH RE asks for,
// so the two look alike on purpose.
//
// The socket is the identity. Bound inside the step's own root, it is reachable
// by that step and by nothing else, and the service behind it holds that step's
// base - so a client needs no token, and there is nothing for it to get wrong.
func (s *Server) withActions(
	root string, ask *Actions, stack []ir.NodeID, body func() error,
) error {
	at := filepath.Join(root, filepath.Clean("/"+ask.Socket))

	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil { //nolint:gosec // reachable by the step
		return fmt.Errorf("make somewhere for the action socket: %w", err)
	}

	// **A unix socket path is not a path**, and `ListenForFills` learned this
	// first: `sun_path` is a fixed-size field, and a longer path fails with
	// `invalid argument` - which names neither the limit nor the length, and
	// sends a reader looking at permissions.
	if len(at) >= sunPathMax {
		return fmt.Errorf(
			"the action socket path is %d bytes and the limit is %d"+
				"\n  %s"+
				"\n  a unix socket lives in a fixed-size field, so ask for one somewhere"+
				" short like /run/earthbuild/actions.sock",
			len(at), sunPathMax-1, at)
	}

	// A socket left by an earlier step in a reused filesystem would be bound
	// over, and `net.Listen` refuses rather than replacing it.
	_ = os.Remove(at)

	ln, err := net.Listen("unix", at)
	if err != nil {
		return fmt.Errorf("listen for actions on %s: %w", ask.Socket, err)
	}

	var hold func() func()
	if s.Idle != nil {
		hold = s.Idle.Hold
	}

	g := grpc.NewServer(grpc.ForceServerCodec(remote.Codec()))
	(&remote.Service{
		Cache: &remote.Cache{
			Store: store.DirStore(s.LayerDir),
			// **Held open while an action runs.** Idleness is measured by when
			// a host last spoke, and a client inside a step is not the host -
			// so without this the machine stops itself while it is busiest, and
			// the client sees a connection close saying nothing.
			//
			// Nil where this server has no idle rule, which is every test and
			// every worker that is not a sandbox: there is nothing to hold open.
			Hold: hold,
		},
		Runner: stepRunner{s: s, stack: stack},
	}).Register(g)

	go func() { _ = g.Serve(ln) }()

	// **Stopped when the step is, not when the build is.** The service exists
	// because a step asked for it; one outliving its step would be answering
	// for a filesystem that has been released.
	defer g.Stop()

	return body()
}

// baseOf is the stack a handle was materialised from.
func (s *Server) baseOf(handle string) []ir.NodeID {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.bases[handle]
}

// stepRunner runs an action over the base of the step that asked for it.
//
// **The default and not the only answer.** Where an action names a
// `container-image` this guest has a stack for, that wins - running it in the
// caller's environment while keying it under the image it named is exactly the
// false hit I3 forbids. An image with no stack here is refused rather than
// substituted: this guest holds layers by digest and has no registry, so there
// is nothing honest it could run instead.
type stepRunner struct {
	s     *Server
	stack []ir.NodeID
}

func (r stepRunner) RunAction(ctx context.Context, action ir.NodeID) (layer.Result, error) {
	return r.s.RunAction(ctx, action, r.stack)
}
