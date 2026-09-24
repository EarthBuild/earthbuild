package remote_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/remote"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A machine with a request in flight is not idle, over gRPC as over HTTP.
//
// **A sandbox stops itself when nobody has wanted it for a while, and idleness
// is measured by when a *host* last spoke.** A client inside a step is not the
// host, so without a hold the machine stops itself while it is busiest - and an
// action is the longest thing this service does, so it is the request most
// likely to be running when the countdown expires. The client sees a connection
// close saying nothing.
//
// The HTTP handler took the hold from the beginning. Nothing on the gRPC path
// did, which is the half that runs actions.
func TestEveryGRPCCallHoldsTheMachineOpen(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	var held, released atomic.Int64

	c := &remote.Cache{
		Store: store.DirStore(t.TempDir()),
		Hold: func() func() {
			held.Add(1)

			return func() { released.Add(1) }
		},
	}

	r := &ran{res: layer.Result{Root: ir.DigestOf([]byte("a tree"))}}
	conn := dialRunning(t, c, r)

	// Execute first, because it is the one that matters: running an action is
	// the longest thing this service does.
	_ = executeOnce(t, conn, layer.EncodeExecuteForTest(ir.DigestOf([]byte("an action")), false))

	if held.Load() == 0 {
		t.Error("running an action did not hold the machine open, so a sandbox" +
			" can stop itself part-way through one")
	}

	// And the unary calls, which are how a client gets its blobs there in the
	// first place: a machine that stopped during an upload is a client that
	// sent everything and has nothing to show for it.
	before := held.Load()

	var out []byte

	in := layer.EncodeFindMissingBlobs([]layer.Blob{{ID: ir.DigestOf([]byte("x")), Size: 1}})

	err := conn.Invoke(context.Background(),
		"/build.bazel.remote.execution.v2.ContentAddressableStorage/FindMissingBlobs",
		&in, &out)
	if err != nil {
		t.Fatal(err)
	}

	if held.Load() == before {
		t.Error("a unary call did not hold the machine open")
	}

	// **Released, or the machine never stops at all.** A hold that is taken and
	// not given back is a sandbox that outlives every build on the host, which
	// is the failure the idle rule exists to prevent, arrived at from the other
	// side.
	if got, want := released.Load(), held.Load(); got != want {
		t.Errorf("%d holds were taken and %d released", want, got)
	}
}
