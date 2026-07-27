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

func newInflight(firstCtx context.Context) (context.Context, *inflight) {
	execCtx, cancel := context.WithCancelCause(context.WithoutCancel(firstCtx))

	inf := &inflight{
		cancel: cancel,
	}

	_ = inf.add(firstCtx)

	return execCtx, inf
}

// close unregisters cancellation listeners and prevents further context additions.
func (inf *inflight) close() {
	inf.mu.Lock()
	defer inf.mu.Unlock()

	if inf.closed {
		return
	}

	inf.closed = true

	for _, stop := range inf.stops {
		stop()
	}

	inf.stops = nil
}

func (inf *inflight) onSubDone(ctx context.Context) {
	inf.mu.Lock()
	defer inf.mu.Unlock()

	if inf.closed {
		return
	}

	inf.numDone++
	if inf.numDone == 1 {
		inf.firstDoneErr = ctx.Err()
	}

	if inf.numDone == len(inf.stops) {
		inf.closed = true

		err := inf.firstDoneErr
		if err == nil {
			err = context.Canceled
		}

		inf.cancel(err)
	}
}

// add adds a new context to the in-flight load. It returns a non-nil error if the
// context is already done or closed ([errClosed]); in both cases ctx has NOT
// been added and its cancellation will not be observed.
func (inf *inflight) add(ctx context.Context) error {
	inf.mu.Lock()
	defer inf.mu.Unlock()

	if inf.closed {
		if inf.firstDoneErr != nil {
			return inf.firstDoneErr
		}

		return errClosed
	}

	err := ctx.Err()
	if err != nil {
		return err
	}

	stop := context.AfterFunc(ctx, func() {
		inf.onSubDone(ctx)
	})

	inf.stops = append(inf.stops, stop)

	return nil
}
