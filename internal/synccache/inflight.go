package synccache

import (
	"context"
	"errors"
	"sync"
)

// errClosed is returned by [inflight.add] once the load context has been closed.
var errClosed = errors.New("inflight load is closed")

// inflight manages cancellation for an in-flight load shared across callers.
// The underlying context is canceled only when all caller contexts are done.
type inflight struct {
	firstDoneErr error
	cancel       context.CancelCauseFunc
	stops        []func() bool
	numDone      int
	mu           sync.Mutex
	closed       bool
}

// newInflight begins tracking a load shared on behalf of firstCtx, and returns the
// context that load should run under.
//
// That context deliberately does not inherit firstCtx's cancellation — the load has to
// outlive any single caller. Two further consequences of WithoutCancel are worth knowing:
// the load context carries no deadline (a caller's timeout still reaches the load, but as
// a cancellation, not as a Deadline a callee can read and forward — gRPC, for one, derives
// its timeout header from that), and its values are firstCtx's for the whole load, even
// once firstCtx itself is gone.
func newInflight(firstCtx context.Context) (context.Context, *inflight) {
	execCtx, cancel := context.WithCancelCause(context.WithoutCancel(firstCtx))

	inf := &inflight{
		cancel: cancel,
	}

	// A first caller that is already gone leaves nothing to watch: add registers no stop,
	// so onSubDone can never fire and the load would run to completion — uncancellable,
	// on behalf of nobody. Treat it as doomed from the outset instead.
	err := inf.add(firstCtx)
	if err != nil {
		inf.mu.Lock()
		inf.closed = true
		inf.firstDoneErr = err
		inf.mu.Unlock()

		cancel(err)
	}

	return execCtx, inf
}

// close unregisters cancellation listeners, releases the shared load context, and
// prevents further context additions.
func (i *inflight) close() {
	i.mu.Lock()

	if i.closed {
		i.mu.Unlock()

		return
	}

	i.closed = true

	for _, stop := range i.stops {
		stop()
	}

	i.stops = nil
	i.mu.Unlock()

	// The load is over, so release the load context rather than leave it live for the
	// rest of the process — its parent is WithoutCancel, so nothing else ever will.
	// Outside the lock: cancel runs whatever the loader registered on it, synchronously.
	i.cancel(errClosed)
}

func (i *inflight) onSubDone(ctx context.Context) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.closed {
		return
	}

	i.numDone++
	if i.numDone == 1 {
		i.firstDoneErr = ctx.Err()
	}

	if i.numDone == len(i.stops) {
		i.closed = true

		err := i.firstDoneErr
		if err == nil {
			err = context.Canceled
		}

		i.cancel(err)
	}
}

// add adds a new context to the in-flight load. It returns a non-nil error if the
// context is already done or closed ([errClosed]); in both cases ctx has NOT
// been added and its cancellation will not be observed.
func (i *inflight) add(ctx context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.closed {
		if i.firstDoneErr != nil {
			return i.firstDoneErr
		}

		return errClosed
	}

	err := ctx.Err()
	if err != nil {
		return err
	}

	stop := context.AfterFunc(ctx, func() {
		i.onSubDone(ctx)
	})

	i.stops = append(i.stops, stop)

	return nil
}
