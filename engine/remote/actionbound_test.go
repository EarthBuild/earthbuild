package remote_test

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/remote"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// counted runs as many actions at once as it is asked to, and remembers the most.
type counted struct {
	now, most atomic.Int64
	release   chan struct{}
}

func (c *counted) RunAction(context.Context, ir.NodeID) (layer.Result, error) {
	n := c.now.Add(1)
	for {
		most := c.most.Load()
		if n <= most || c.most.CompareAndSwap(most, n) {
			break
		}
	}

	<-c.release
	c.now.Add(-1)

	return layer.Result{Root: ir.DigestOf([]byte("a tree"))}, nil
}

// Actions have a bound of their own.
//
// **A client decides how many actions to ask for, and this one is inside a
// sandbox.** Buck2 sizes its own parallelism from the machine it thinks it is
// on, so a step given an execution service can ask for as many concurrent
// actions as it likes - and every one of them is a process on a machine that is
// already running the step that asked.
//
// Their own bound rather than the build's: the two never share a pool, because
// a step waiting on an action that cannot start is a build waiting for itself.
// Separate pools can oversubscribe a machine, which is slow, and prefer slow -
// a deadlock needs a person and a stack dump, oversubscription needs patience
// (plan-remote-execution R5).
func TestActionsAreBounded(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	const (
		bound = 2
		asked = 8
	)

	r := &counted{release: make(chan struct{})}

	conn := dialBounded(t, &remote.Cache{Store: store.DirStore(t.TempDir())}, r, bound)

	var wg sync.WaitGroup

	for i := range asked {
		wg.Go(func() {
			id := ir.DigestOf([]byte{byte(i)})
			_ = executeOnce(t, conn, layer.EncodeExecuteForTest(id, false))
		})
	}

	// Let them pile up against the bound before any is allowed to finish, so
	// what is measured is how many the service admitted rather than how fast
	// the machine happened to be.
	waitUntil(t, func() bool { return r.most.Load() >= bound })

	close(r.release)
	wg.Wait()

	if got := r.most.Load(); got > bound {
		t.Errorf("%d actions ran at once against a bound of %d", got, bound)
	}

	// A bound that admitted nothing would pass the check above and is not a
	// bound, it is a stall.
	if r.most.Load() == 0 {
		t.Error("no action ran at all")
	}
}

// dialBounded is dialRunning with a bound on how many actions may run at once.
func dialBounded(t *testing.T, c *remote.Cache, r remote.Runner, bound int) *grpc.ClientConn {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	g := grpc.NewServer(grpc.ForceServerCodec(remote.Codec()))
	(&remote.Service{Cache: c, Runner: r, MaxActions: bound}).Register(g)

	go func() { _ = g.Serve(ln) }()
	t.Cleanup(g.Stop)

	conn, err := grpc.NewClient(ln.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(remote.Codec())))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// waitUntil blocks until a condition holds, or the test has waited long enough
// to call it a failure rather than a slow machine.
func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()

	for range 1000 {
		if ok() {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatal("the condition never held, so the service admitted fewer actions" +
		" than its bound and nothing is being measured")
}
