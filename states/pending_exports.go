package states

import "context"

// PendingExports keeps every ExportOutcome a target is waiting on, so that a
// build that stops before an export has run can still end the targets waiting
// on it. A nil *PendingExports keeps nothing.
type PendingExports struct{}

// Abort settles every kept outcome that is still pending, as cancelled because
// of cause.
//
// TODO: not implemented yet; the next commit implements it.
func (p *PendingExports) Abort(_ context.Context, _ error) {}
