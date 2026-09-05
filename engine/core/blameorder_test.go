package core

import (
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

// Two files are not comparable by line, so graph order decides.
func TestDifferentFilesFallBackToGraphOrder(t *testing.T) {
	t.Parallel()

	_, err := worseFailure(
		&StepError{Source: "b/Earthfile:2", Exit: 1}, 5,
		&StepError{Source: "a/Earthfile:99", Exit: 1}, 1,
	)

	if !strings.Contains(err.Error(), "a/Earthfile:99") {
		t.Errorf("two files should fall back to graph order, got: %v", err)
	}
}
