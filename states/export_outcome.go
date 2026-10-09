package states

import (
	"context"
	"sync"
)

// ExportOutcome records how an image export ended, and calls back whoever was
// waiting for it. A target whose main state is solved only by exporting one of
// its images, rather than by force executing it, has not executed until that
// export is done. Its success side effects (such as saving a BUILD --auto-skip
// hash) wait here.
//
// The zero value is ready to use. Only the first outcome counts.
type ExportOutcome struct {
	err     error
	waiting []func(context.Context, error)
	mu      sync.Mutex
	done    bool
}

// Then arranges for fn to be called with the export's error once the export is
// done. If it is already done, fn is called straight away.
func (o *ExportOutcome) Then(ctx context.Context, fn func(context.Context, error)) {
	o.mu.Lock()

	if !o.done {
		o.waiting = append(o.waiting, fn)
		o.mu.Unlock()

		return
	}

	err := o.err
	o.mu.Unlock()

	fn(ctx, err)
}

// Settle records that the export is done, with err as its outcome, and calls
// every function registered with Then. Calls after the first are ignored.
func (o *ExportOutcome) Settle(ctx context.Context, err error) {
	o.mu.Lock()

	if o.done {
		o.mu.Unlock()
		return
	}

	o.done = true
	o.err = err
	waiting := o.waiting
	o.waiting = nil
	o.mu.Unlock()

	for _, fn := range waiting {
		fn(ctx, err)
	}
}
