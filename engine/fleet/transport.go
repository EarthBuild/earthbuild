package fleet

import (
	"context"
	"errors"
)

// The three protocols of C.2, by their ALPN.
//
// Separate protocols rather than message kinds on one stream, and the reason is
// C.4: blobs move in batches and a thousand-blob synchronisation must not be a
// thousand streams competing with the control traffic that decides what to fetch
// next. A heartbeat behind a gigabyte of layer is a worker presumed dead.
const (
	// ALPNControl carries claim, heartbeat, result and cancel.
	ALPNControl = "earth/ctl/1"
	// ALPNBlob carries content-addressed transfer, verified per chunk.
	ALPNBlob = "earth/blob/1"
	// ALPNMask carries mask and profile exchange - the hints of I5, which any
	// participant may drop without affecting a result.
	ALPNMask = "earth/mask/1"
)

// ErrNoWorker is a claim that found nobody.
var ErrNoWorker = errors.New("no worker is available")

// ErrWorkerGone is a worker that stopped answering mid-step.
//
// **Not a build failure.** A step is pure (I1), so one that vanished with its
// worker can be run again anywhere - which is the same property that makes retry
// safe (I7) and is why C.5 can say a disappearance costs a re-queue rather than
// a result.
var ErrWorkerGone = errors.New("the worker stopped answering")

// Transport is how a driver reaches a worker.
//
// An interface because the fleet has to be testable before it has a network:
// every property C.3 and C.5 state - that an assignment reaches exactly one
// worker, that a cancel stops it, that a disappearance re-queues rather than
// fails - is about the *protocol*, and a test that had to boot two processes to
// check them would be run rarely enough not to catch anything.
//
// go-iroh supplies the real one. This is the shape it has to fit.
type Transport interface {
	// Assign gives a step to a worker and waits for its reply.
	//
	// One worker, exactly. C.3's assignment is a claim of work, and two workers
	// running one step is not wrong - steps are pure - but it is waste, and
	// waste that grows with the fleet.
	Assign(ctx context.Context, a Assignment) (Reply, error)
	// Workers is how many are currently reachable, for the scheduler's placement
	// decisions. Advisory: a number that is stale by the time it is read, which
	// is why nothing may depend on it for correctness (I5).
	Workers() int
}
