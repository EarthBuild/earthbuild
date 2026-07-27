// Package metacontext provides a context implementation enriched with earth-specific metadata and deadlines.
package metacontext

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

var _ context.Context = &MetaContext{}

// ErrClosed is returned by [MetaContext.Add] once the MetaContext has been closed.
var ErrClosed = errors.New("metacontext is closed")

// MetaContext is an object which implements context.Context and which holds multiple
// contexts within it. The MetaContext is considered canceled only when ALL of the
// underlying contexts have been canceled.
//
// Once canceled, it cannot be uncancelled, so it is an error to keep adding contexts
// once the meta context is considered cancelled.
//
// A MetaContext owns goroutines — one monitor, plus one watcher per [MetaContext.Add] —
// and they live until either every sub-context is canceled or [MetaContext.Close] is
// called. They hold the MetaContext and every sub-context, so dropping your pointer to a
// MetaContext does not make it collectable. Whoever creates one is responsible for
// closing it.
type MetaContext struct {
	firstDoneErr error
	subDoneCh    chan int // index
	doneCh       chan struct{}
	stopCh       chan struct{} // closed by Close; releases monitor and all watchers
	sub          []context.Context
	numDone      int
	mu           sync.Mutex
	firstDoneMu  sync.Mutex
	closeOnce    sync.Once
}

// New returns a new metacontext. The caller must [MetaContext.Close] it once the work it
// is keeping alive has finished, or its goroutines are retained for the life of the
// process.
func New(ctx context.Context) *MetaContext {
	mc := &MetaContext{
		doneCh:    make(chan struct{}),
		subDoneCh: make(chan int),
		stopCh:    make(chan struct{}),
		sub:       make([]context.Context, 0, 4),
	}

	_ = mc.Add(ctx)
	go mc.monitor() //nolint:contextcheck

	return mc
}

// Close releases the goroutines watching this MetaContext. It is idempotent and safe to
// call concurrently with Add.
//
// After Close the MetaContext stops tracking cancellation: Done will not fire, Err will
// not change, and Add returns [ErrClosed]. Deadline and Value keep working. Close only
// once nothing is still relying on the MetaContext to observe cancellation.
func (mc *MetaContext) Close() {
	mc.closeOnce.Do(func() {
		close(mc.stopCh)
	})
}

func (mc *MetaContext) monitor() {
	for {
		var index int

		select {
		case <-mc.stopCh:
			return
		case index = <-mc.subDoneCh:
		}

		mc.mu.Lock()

		mc.numDone++
		if mc.numDone == 1 {
			firstDoneCtx := mc.sub[index]
			mc.firstDoneMu.Lock()

			go func() {
				// Call .Err() outside of our lock. Also, use a different lock
				// to block a caller to our .Err if it'll take a long time.
				defer mc.firstDoneMu.Unlock()

				err := firstDoneCtx.Err()
				mc.firstDoneErr = err
			}()
		}

		if mc.numDone == len(mc.sub) {
			close(mc.doneCh)
			mc.mu.Unlock()

			return
		}

		mc.mu.Unlock()
	}
}

// Add adds a new context to the metacontext. It returns a non-nil error if the
// MetaContext is already done (the first sub-context's error) or closed ([ErrClosed]); in
// both cases ctx has NOT been added and its cancellation will not be observed.
func (mc *MetaContext) Add(ctx context.Context) error {
	mc.mu.Lock()

	select {
	case <-mc.stopCh:
		mc.mu.Unlock()

		return ErrClosed
	default:
	}

	select {
	case <-mc.doneCh:
		mc.mu.Unlock()

		mc.firstDoneMu.Lock()
		defer mc.firstDoneMu.Unlock()

		if mc.firstDoneErr != nil {
			return mc.firstDoneErr
		}

		// Mirror Err: done always means something, never a nil error, so callers can
		// rely on "Add returned non-nil" meaning "not added".
		return context.Canceled
	default:
	}

	mc.sub = append(mc.sub, ctx)
	index := len(mc.sub) - 1
	mc.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
		case <-mc.stopCh:
			return
		}

		select {
		case mc.subDoneCh <- index:
		case <-mc.stopCh:
		}
	}()

	return nil
}

// Deadline returns the earliest Deadline in the pool.
func (mc *MetaContext) Deadline() (deadline time.Time, ok bool) {
	mc.mu.Lock()

	n := len(mc.sub)
	if n == 0 {
		mc.mu.Unlock()

		return time.Time{}, false
	}

	// Copy contexts to evaluate deadlines without holding the lock.
	// Use a stack-allocated buffer for typical sub-context counts to avoid heap allocation.
	var (
		stackBuf [8]context.Context
		contexts []context.Context
	)

	if n <= len(stackBuf) {
		copy(stackBuf[:], mc.sub)
		contexts = stackBuf[:n]
	} else {
		contexts = slices.Clone(mc.sub)
	}

	mc.mu.Unlock()

	for _, ctx := range contexts {
		dl, deadlineOk := ctx.Deadline() // don't hold lock for this call
		if deadlineOk {
			if !ok || dl.Before(deadline) {
				deadline = dl
			}

			ok = true
		}
	}

	return deadline, ok
}

// Done returns the done channel. The MetaContext is done only when ALL of the
// contained contexts are done.
func (mc *MetaContext) Done() <-chan struct{} {
	return mc.doneCh
}

// Err returns the first done error reported by any context, if the whole
// context is done. Nil otherwise.
func (mc *MetaContext) Err() error {
	select {
	case <-mc.doneCh:
		mc.firstDoneMu.Lock()
		defer mc.firstDoneMu.Unlock()

		if mc.firstDoneErr != nil {
			return mc.firstDoneErr
		}

		return context.Canceled
	default:
		return nil
	}
}

// Value calls context.Value on the first not-done context, or on the first one,
// if all are done.
func (mc *MetaContext) Value(key any) any {
	mc.mu.Lock()

	if len(mc.sub) == 0 {
		mc.mu.Unlock()
		return nil
	}
	// Find the first not-done ctx. If none found, use the first one.
	var selectedCtx context.Context

	for _, ctx := range mc.sub {
		select {
		case <-mc.doneCh:
			continue
		default:
		}

		selectedCtx = ctx

		break
	}

	if selectedCtx == nil {
		selectedCtx = mc.sub[0]
	}

	mc.mu.Unlock()

	return selectedCtx.Value(key) // don't hold lock for this call
}
