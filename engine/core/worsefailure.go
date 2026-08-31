package core

import (
	"context"
	"errors"
	"strconv"
	"strings"
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

	// **Source position before graph position.** Graph order is deterministic
	// and means nothing to a reader: `g.Nodes()` breaks ties by node identity,
	// so four sibling steps that all fail are ranked by a hash and the author is
	// told about whichever it preferred - stably, and unactionably (E934). The
	// order an author reads in is the order to blame in.
	//
	// Only within one file. Two files have no order between them that a reader
	// would recognise, so those fall through to the graph, which at least does
	// not change between runs.
	curFile, curLine, curOK := sourceAt(cur)
	nextFile, nextLine, nextOK := sourceAt(next)

	if curOK && nextOK && curFile == nextFile && curLine != nextLine {
		if nextLine < curLine {
			return nextAt, next
		}

		return curAt, cur
	}

	if nextAt < curAt {
		return nextAt, next
	}

	return curAt, cur
}

// sourceAt is the file and line a failure names, if it names one.
//
// **Parsed, not compared as text.** `Earthfile:10` sorts before `Earthfile:4`
// as a string, which would swap exactly the pair a reader most often has: any
// file longer than nine lines.
//
// The last colon separates them, because a path may contain one and a line
// number may not.
func sourceAt(err error) (file string, line int, ok bool) {
	var step *StepError
	if !errors.As(err, &step) || step.Source == "" {
		return "", 0, false
	}

	return splitSource(step.Source)
}

// isCancellation reports whether an error is the build being stopped rather than
// a step going wrong.
//
// Both, because a deadline and an explicit cancel are the same news here: this
// step did not fail, it was not allowed to finish.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// splitSource is sourceAt for a step that has not failed yet, so has no error to
// read the position out of.
func splitSource(src string) (file string, line int, ok bool) {
	if src == "" {
		return "", 0, false
	}

	at := strings.LastIndex(src, ":")
	if at < 0 {
		return src, 0, false
	}

	n, err := strconv.Atoi(src[at+1:])
	if err != nil {
		return src[:at], 0, false
	}

	return src[:at], n, true
}
