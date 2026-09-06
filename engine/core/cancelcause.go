package core

import (
	"context"
	"errors"
	"fmt"
)

// ErrSuperseded is a cancellation that means nothing went wrong.
//
// **A good cancel.** The same step given to two machines is the design working:
// one arrives first, the other is stopped, and the work it was doing is simply no
// longer needed. Speculative work is the same shape - a guess that stopped being
// worth finishing.
//
// It exists as a distinct cause because a build cannot tell the two apart from
// the outside: `context canceled` is what a step reports whether it was
// superseded or whether the build is collapsing around it, and treating a good
// cancel as a fault turns a working optimisation into a red build.
var ErrSuperseded = errors.New("superseded: another worker produced this result first")

// CancelledError is a step stopped before it finished, and why.
//
// **The cause, not the cancellation.** A step that reports `context canceled`
// has told the author the one thing they already know - it stopped - and
// withheld the only thing they can act on, which is what went wrong somewhere
// else. That is the failing this type exists to remove, and the reason a
// cancelled buildkit build sends you hunting through logs for the one step that
// actually failed.
type CancelledError struct {
	// Source is the step that was stopped.
	Source string
	// Cause is why: a root failure, or ErrSuperseded where nothing went wrong.
	Cause error
}

func (e *CancelledError) Error() string {
	if errors.Is(e.Cause, ErrSuperseded) {
		return fmt.Sprintf("%s was not needed: %v", e.Source, e.Cause)
	}

	return fmt.Sprintf("%s was stopped because %v", e.Source, e.Cause)
}

// Unwrap exposes the cause, so `errors.As` reaches the root failure and a caller
// can ask what kind it was rather than reading the sentence.
func (e *CancelledError) Unwrap() error { return e.Cause }

// cancelled explains a stopped step in terms of what stopped it.
//
// A cause of nil means nobody recorded one, which is itself worth saying plainly
// rather than dressing a bare cancellation up as an explanation.
func cancelled(source string, cause error) error {
	if cause == nil {
		cause = context.Canceled
	}

	return &CancelledError{Source: source, Cause: cause}
}

// benignCancel reports whether a stopped step means nothing went wrong.
//
// Only an explicit ErrSuperseded qualifies. A bare `context canceled` does not:
// nothing said it was good, and assuming so is how a real fault becomes silence
// - which is the direction that costs a debugging session rather than a red
// build.
func benignCancel(err error) bool {
	return errors.Is(err, ErrSuperseded)
}
