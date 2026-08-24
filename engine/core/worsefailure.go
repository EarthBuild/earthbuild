package core

import (
	"context"
	"errors"
)

// worseFailure picks which of two failures a build should be blamed on.
//
// The base rule is the **earliest in graph order**, so a build blames the same
// command however the goroutines race - a build that names a different step run
// to run is one nobody can act on.
//
// The rule on top of it is that **a cancellation never outranks its own cause**.
// When one step fails, everything still running is cancelled; those steps then
// report `context canceled`, and one of them will often sit earlier in graph
// order than the step that actually failed. Reporting it hands the author a
// consequence in place of a cause, and the cause is the only actionable half.
//
// Two cancellations, or two genuine failures, fall back to graph order.
func worseFailure(cur error, curAt int, next error, nextAt int) (int, error) {
	if cur == nil {
		return nextAt, next
	}

	curCancel, nextCancel := isCancellation(cur), isCancellation(next)

	// Kind first, order second. A real failure displaces a cancellation whatever
	// their positions, and is never displaced by one.
	if curCancel != nextCancel {
		if nextCancel {
			return curAt, cur
		}

		return nextAt, next
	}

	if nextAt < curAt {
		return nextAt, next
	}

	return curAt, cur
}

// isCancellation reports whether an error is the build being stopped rather than
// a step going wrong.
//
// Both, because a deadline and an explicit cancel are the same news here: this
// step did not fail, it was not allowed to finish.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
