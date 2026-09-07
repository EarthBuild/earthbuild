package guest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// blockingMat holds a materialise open until the test lets it go.
type blockingMat struct {
	root    string
	release chan struct{}
	// abandoned closes when the guest gave up on this materialise, which is
	// what says the cancel reached the work rather than only the caller.
	abandoned chan struct{}
}

func (m *blockingMat) Materialise(ctx context.Context, _ []ir.NodeID) (core.Handle, error) {
	select {
	case <-m.release:
		return fixedHandle{m.root}, nil
	case <-ctx.Done():
		close(m.abandoned)

		return nil, ctx.Err()
	}
}

// A cancelled build does not wait for a materialise it no longer wants.
//
// `doStream` takes a context *"that can abandon the wait"*, and the exec path
// passes one. Everything else went through `do`, which called
// `doStream(context.Background(), …)` - so `Materialise`, `Capture`, `Export`
// and `Copy` waited on a context nobody could cancel. Four of them take a
// `context.Context` and named it `_`, which is the shape of the defect written
// down: a signature promising cancellation and a body discarding it.
//
// It matters where the wait is long. A materialise of a deep stack or a capture
// of a large layer is exactly when somebody presses Ctrl-C, and exactly when the
// answer was "not until it finishes".
func TestACancelledCallDoesNotWaitForTheGuest(t *testing.T) {
	t.Parallel()

	mat := &blockingMat{
		root:      t.TempDir(),
		release:   make(chan struct{}),
		abandoned: make(chan struct{}),
	}
	c := pairWith(t, &guest.Server{Mat: mat, Unconfined: true})

	// Released at the end so the server's goroutine is not left blocked.
	t.Cleanup(func() { close(mat.release) })

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)

	go func() {
		_, err := c.Materialise(ctx, nil)
		done <- err
	}()

	// Long enough for the request to have reached the server and be waiting.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the call returned %v, not a cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled materialise was still waiting five seconds later;" +
			" the context reached no further than the signature")
	}

	// And the guest stopped too.
	//
	// Releasing the caller is not the same as cancelling the work: the client
	// sends a KindCancel before it returns, and the guest could only act on it
	// for an exec, where `began` had registered a kill. Every other kind found
	// nothing registered and ran to completion with its reply dropped (E177a).
	//
	// A request that nobody is waiting for is work a build is paying for and
	// will not use - a materialise of a deep stack, a capture of a large layer -
	// so the guest now registers a cancel for every request, not only a step.
	select {
	case <-mat.abandoned:
	case <-time.After(5 * time.Second):
		t.Error("the caller was released and the guest carried on materialising;" +
			" a cancel that only reaches the client is a wait avoided, not work stopped")
	}
}
