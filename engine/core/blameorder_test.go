package core

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// The blamed step is the earliest in the Earthfile, not the earliest by hash.
//
// **Graph order is deterministic and means nothing to a reader.** `g.Nodes()` is
// post-order with ties broken by node identity, so four sibling leaves that all
// fail are ranked by a hash: the author is told about whichever of four
// equally-failing lines it preferred, and told about a different one when a node
// changes. That is stable and unactionable, which is the pair E934 is about.
//
// Source position is the order the author reads in, so it is the order to blame
// in. Graph order remains the tie-break, for steps with no position or two steps
// on one line.
func TestTheEarlierLineIsBlamed(t *testing.T) {
	t.Parallel()

	early := &StepError{Source: "Earthfile:4", Exit: 1}
	late := &StepError{Source: "Earthfile:10", Exit: 1}

	// Graph order deliberately disagrees with source order: `late` is at index
	// 0 and would win under the old rule.
	at, err := worseFailure(late, 0, early, 9)
	if !strings.Contains(err.Error(), "Earthfile:4") {
		t.Errorf("blamed the later line: %v (at %d)", err, at)
	}

	// And the same the other way round, so it is the position deciding rather
	// than the argument order.
	at, err = worseFailure(early, 9, late, 0)
	if !strings.Contains(err.Error(), "Earthfile:4") {
		t.Errorf("blamed the later line when it arrived second: %v (at %d)", err, at)
	}
}

// Ten is after four, which string comparison gets wrong.
//
// `Earthfile:10` sorts before `Earthfile:4` as text, so comparing the sources as
// strings would swap exactly the pair a reader most often has - a file with more
// than nine lines.
func TestLineNumbersCompareAsNumbers(t *testing.T) {
	t.Parallel()

	_, err := worseFailure(
		&StepError{Source: "Earthfile:10", Exit: 1}, 0,
		&StepError{Source: "Earthfile:9", Exit: 1}, 1,
	)

	if !strings.Contains(err.Error(), "Earthfile:9") {
		t.Errorf("compared line numbers as text: %v", err)
	}
}

// Two files are ordered by name, because *some* total order is required.
//
// Graph order was the obvious answer and is the wrong one: combined with the
// line rule it is intransitive, so a fold over three failures in two files
// blames whichever arrived first - see
// TestTheBlamedStepDoesNotDependOnArrivalOrder. A reader recognises no order
// between two files, so the choice between them is arbitrary; it only has to be
// *stable*, and a file name is the one thing both failures always carry.
func TestDifferentFilesAreOrderedByName(t *testing.T) {
	t.Parallel()

	// Graph order deliberately disagrees: `b/Earthfile:2` is the later name and
	// the earlier graph position.
	_, err := worseFailure(
		&StepError{Source: "b/Earthfile:2", Exit: 1}, 1,
		&StepError{Source: "a/Earthfile:99", Exit: 1}, 5,
	)

	if !strings.Contains(err.Error(), "a/Earthfile:99") {
		t.Errorf("two files should be ordered by name, got: %v", err)
	}

	// And the same when they arrive the other way round.
	_, err = worseFailure(
		&StepError{Source: "a/Earthfile:99", Exit: 1}, 5,
		&StepError{Source: "b/Earthfile:2", Exit: 1}, 1,
	)

	if !strings.Contains(err.Error(), "a/Earthfile:99") {
		t.Errorf("name order changed with arrival order, got: %v", err)
	}
}

// The blamed step does not depend on the order the failures arrived in.
//
// `worseFailure` is folded pairwise over results as they come back, so it has to
// be a total order - a fold over a comparison that is merely *pairwise*
// reasonable gives a different answer for a different arrival order, which is
// the one thing this function exists to prevent:
//
//	a build that names a different step run to run is one nobody can act on
//
// The two rules above are individually right and together intransitive. With
// three failures, two of them sharing a file:
//
//	Earthfile:10        graph 0     same file as Earthfile:5, later line
//	other/Earthfile:1   graph 1     another file
//	Earthfile:5         graph 2     same file as Earthfile:10, earlier line
//
// `Earthfile:5` beats `Earthfile:10` on line; `Earthfile:10` beats
// `other/Earthfile:1` on graph order; `other/Earthfile:1` beats `Earthfile:5` on
// graph order. A cycle, so whichever arrives first wins and a build with
// failures in two files blames a different command run to run (E968).
//
// Needs three failures across two files, which is why it survived: every test
// above it uses two, where any comparison at all is transitive.
func TestTheBlamedStepDoesNotDependOnArrivalOrder(t *testing.T) {
	t.Parallel()

	type failure struct {
		err *StepError
		at  int
	}

	a := failure{&StepError{Source: "Earthfile:10", Exit: 1}, 0}
	b := failure{&StepError{Source: "other/Earthfile:1", Exit: 1}, 1}
	c := failure{&StepError{Source: "Earthfile:5", Exit: 1}, 2}

	// Every order the three could come back in. The scheduler's goroutines
	// decide this, so all six are reachable.
	orders := [][]failure{
		{a, b, c},
		{a, c, b},
		{b, a, c},
		{b, c, a},
		{c, a, b},
		{c, b, a},
	}

	blamed := map[string]string{}

	for _, order := range orders {
		var (
			cur  error
			at   int
			name []string
		)

		for i, f := range order {
			name = append(name, f.err.Source)

			if i == 0 {
				cur, at = f.err, f.at

				continue
			}

			at, cur = worseFailure(cur, at, f.err, f.at)
		}

		blamed[strings.Join(name, ",")] = sourceOf(cur)
	}

	// One answer, whatever the order. Reported in full because *which* orders
	// disagree is the diagnosis: a pair that differs names the two rules in
	// conflict.
	seen := map[string]bool{}
	for _, who := range blamed {
		seen[who] = true
	}

	if len(seen) != 1 {
		t.Errorf("arrival order decided who was blamed - %d different answers:", len(seen))

		for order, who := range blamed {
			t.Errorf("  arriving %s blames %s", order, who)
		}
	}
}

// sourceOf is the position a failure names, for reporting which step was
// blamed.
func sourceOf(err error) string {
	file, line, ok := sourceAt(err)
	if !ok {
		return "no source"
	}

	return file + ":" + strconv.Itoa(line)
}

// Independent failures are all reported; a failure caused by another is not.
//
// Two sibling steps that fail for their own reasons are two things to fix, and
// naming one of them sends the author back for a second build to be told about
// the other. A step that failed *because* an earlier one did is not a second
// thing to fix - it is the same news, restated further down.
//
// Cancellations never appear beside a real failure, for the reason worseFailure
// gives: they are the consequence of the failure being reported, and a
// consequence in place of a cause is the half that cannot be acted on.
func TestIndependentFailuresAreAllReported(t *testing.T) {
	t.Parallel()

	a := &StepError{Source: "Earthfile:5", Exit: 1}
	b := &StepError{Source: "Earthfile:9", Exit: 1}
	downstream := &StepError{Source: "Earthfile:20", Exit: 1}

	caused := map[string]string{"c": "a"} // c failed because a did

	got := independentFailures([]failed{
		{err: b, at: 1, key: "b"},
		{err: downstream, at: 2, key: "c"},
		{err: a, at: 0, key: "a"},
	}, func(of string) (string, bool) { v, ok := caused[of]; return v, ok })

	lines := make([]string, 0, len(got))
	for _, f := range got {
		lines = append(lines, sourceOf(f.err))
	}

	want := "Earthfile:5,Earthfile:9"
	if strings.Join(lines, ",") != want {
		t.Errorf("reported %s, want %s - two independent failures, in source order,"+
			" and not the one they caused", strings.Join(lines, ","), want)
	}
}

// A cancellation is dropped when anything really failed.
func TestACancellationIsNotReportedBesideARealFailure(t *testing.T) {
	t.Parallel()

	genuine := &StepError{Source: "Earthfile:9", Exit: 1}

	got := independentFailures([]failed{
		{err: context.Canceled, at: 0, key: "a"},
		{err: genuine, at: 1, key: "b"},
	}, func(string) (string, bool) { return "", false })

	if len(got) != 1 || sourceOf(got[0].err) != "Earthfile:9" {
		t.Errorf("got %d failures, want only the real one", len(got))
	}
}
