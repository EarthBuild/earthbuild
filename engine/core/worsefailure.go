package core

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
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
	// **Across files, by file name, because a fold needs a total order.**
	// Falling back to the graph there was the obvious answer and is intransitive
	// with the line rule above: `Earthfile:5` beats `Earthfile:10` on line,
	// `Earthfile:10` beats `other/Earthfile:1` on graph position, and
	// `other/Earthfile:1` beats `Earthfile:5` on graph position. Three failures
	// in two files then blame whoever arrived first - which is the one thing
	// this function exists to prevent, and it went unnoticed because two
	// failures cannot form a cycle (E968).
	//
	// A reader recognises no order between two files, so the choice is
	// arbitrary; it only has to be stable, and the name is the one thing both
	// failures always carry.
	if at, ok, chosen := byPosition(cur, curAt, next, nextAt); ok {
		return at, chosen
	}

	if nextAt < curAt {
		return nextAt, next
	}

	return curAt, cur
}

// byPosition compares two failures by where they were written, and says whether
// it could: a failure with no source has no position to compare.
//
// Returns the winner, so the caller's fall-back to graph order is reached only
// when this cannot decide.
func byPosition(cur error, curAt int, next error, nextAt int) (at int, ok bool, err error) {
	curFile, curLine, curOK := sourceAt(cur)
	nextFile, nextLine, nextOK := sourceAt(next)

	if !curOK || !nextOK {
		return 0, false, nil
	}

	if curFile != nextFile {
		if nextFile < curFile {
			return nextAt, true, next
		}

		return curAt, true, cur
	}

	if curLine == nextLine {
		return 0, false, nil
	}

	if nextLine < curLine {
		return nextAt, true, next
	}

	return curAt, true, cur
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
	if _, ok := errors.AsType[*CancelledError](err); ok {
		return true
	}

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

// failed is one step that failed, and where it sits.
//
// key identifies the node, so a failure caused by another can be recognised
// without this file knowing what a node is.
type failed struct {
	err error
	at  int
	key string
}

// independentFailures is every failure worth telling the author about, in the
// order they would read them.
//
// **All of them, when they are independent.** Two sibling steps that fail for
// their own reasons are two things to fix, and naming one sends the author back
// for a second build to be told about the other. The build already ran both.
//
// **And only the cause, when one produced the other.** A step that failed
// because an earlier one did is the same news restated further down; `causedBy`
// answers which failure a step's own failure descends from, if any.
//
// Cancellations are dropped as soon as anything really failed, for the reason
// worseFailure gives: they are the consequence of the failure being reported,
// and a consequence in place of a cause is the half nobody can act on. Where
// *everything* was cancelled they are all there is, so they are what is
// reported.
func independentFailures(all []failed, causedBy func(key string) (string, bool)) []failed {
	// **Superseded work is dropped outright.** Every other cancellation survives
	// the empty case below, because a build that failed has to say something; a
	// superseded step is different in kind - it did not fail, it stopped being
	// needed - so a build made entirely of them did not go wrong (E969).
	kept := make([]failed, 0, len(all))

	for _, f := range all {
		if !benignCancel(f.err) {
			kept = append(kept, f)
		}
	}

	genuine := make([]failed, 0, len(kept))

	for _, f := range kept {
		if !isCancellation(f.err) {
			genuine = append(genuine, f)
		}
	}

	if len(genuine) == 0 {
		genuine = kept
	}

	// Which of the failures are themselves a cause, so a step descending from
	// one can be recognised as its echo.
	isFailure := make(map[string]bool, len(genuine))
	for _, f := range genuine {
		isFailure[f.key] = true
	}

	out := make([]failed, 0, len(genuine))

	for _, f := range genuine {
		if cause, ok := causedBy(f.key); ok && isFailure[cause] {
			continue
		}

		out = append(out, f)
	}

	// The order the author reads in, by the same rule that picks a single
	// failure - so one failure and several are ordered by one definition.
	sort.SliceStable(out, func(i, j int) bool {
		at, _ := worseFailure(out[i].err, out[i].at, out[j].err, out[j].at)

		return at == out[i].at
	})

	return out
}

// reportFailures turns everything that failed into the error a build reports.
//
// One failure returns itself, unchanged, because that is almost every build and
// nothing downstream should have to learn a new shape for it. Several
// independent ones return a MultiStepError, which reports them in the order the
// author reads them in.
func reportFailures(all []failed, nodes []*ir.Node) error {
	byID := make(map[string]*ir.Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID().String()] = n
	}

	failing := make(map[string]bool, len(all))
	for _, f := range all {
		failing[f.key] = true
	}

	// Which failure a step's own failure descends from, walking what it stood
	// on and what it read. A step cannot fail *because* of another unless the
	// other is upstream of it.
	causedBy := func(key string) (string, bool) {
		n, ok := byID[key]
		if !ok {
			return "", false
		}

		seen := map[string]bool{key: true}
		queue := append(append([]*ir.Node{}, n.Inputs...), n.Sources...)

		for len(queue) > 0 {
			up := queue[0]
			queue = queue[1:]

			id := up.ID().String()
			if seen[id] {
				continue
			}

			seen[id] = true

			if failing[id] {
				return id, true
			}

			queue = append(append(queue, up.Inputs...), up.Sources...)
		}

		return "", false
	}

	report := independentFailures(all, causedBy)
	if len(report) == 1 {
		return report[0].err
	}

	errs := make([]error, 0, len(report))
	for _, f := range report {
		errs = append(errs, f.err)
	}

	return &MultiStepError{Steps: errs}
}

// MultiStepError is more than one independent step failing in one build.
//
// **A type rather than a joined string**, so `errors.As` still finds a
// `*StepError` and every caller that looked for one keeps working - it just now
// finds the first of several rather than the only one.
type MultiStepError struct {
	Steps []error
}

func (f *MultiStepError) Error() string {
	parts := make([]string, 0, len(f.Steps))
	for _, e := range f.Steps {
		parts = append(parts, e.Error())
	}

	return strings.Join(parts, "\n")
}

// Unwrap gives errors.Is and errors.As every failure, not just the first.
func (f *MultiStepError) Unwrap() []error { return f.Steps }
