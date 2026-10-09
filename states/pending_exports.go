package states

import (
	"context"
	"fmt"
	"sync"
)

// PendingExports keeps every ExportOutcome a target is waiting on, so that a
// build that stops before an export has run can still end the targets waiting
// on it. A nil *PendingExports keeps nothing.
type PendingExports struct {
	// abortErr is what Abort settled the outcomes with; nil until then.
	abortErr error
	outcomes []*ExportOutcome
	mu       sync.Mutex
}

// Then arranges for fn to be called once outcome is settled, as outcome.Then
// does, and keeps outcome so that Abort can settle it. After Abort, outcome is
// settled straight away, unless it already was.
func (p *PendingExports) Then(ctx context.Context, outcome *ExportOutcome, fn func(context.Context, error)) {
	abortErr := p.keep(outcome)
	if abortErr != nil {
		outcome.Settle(ctx, abortErr)
	}

	outcome.Then(ctx, fn)
}

// keep keeps outcome for Abort, unless Abort has already run, in which case it
// returns the error Abort settled the others with.
func (p *PendingExports) keep(outcome *ExportOutcome) error {
	if p == nil {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.abortErr == nil {
		p.outcomes = append(p.outcomes, outcome)
	}

	return p.abortErr
}

// Abort settles every kept outcome that is still pending, as cancelled because
// of cause: the targets waiting on them did not fail themselves, the build
// stopped before their export ran. The error it settles them with wraps
// context.Canceled and cause. An outcome that is already settled keeps its
// result, and only the first Abort counts.
func (p *PendingExports) Abort(ctx context.Context, cause error) {
	if p == nil {
		return
	}

	abortErr := fmt.Errorf("%w: the build stopped before this image was exported", context.Canceled)
	if cause != nil {
		abortErr = fmt.Errorf("%w: %w", abortErr, cause)
	}

	p.mu.Lock()

	if p.abortErr != nil {
		p.mu.Unlock()
		return
	}

	p.abortErr = abortErr
	outcomes := p.outcomes
	p.outcomes = nil
	p.mu.Unlock()

	for _, outcome := range outcomes {
		outcome.Settle(ctx, abortErr)
	}
}
