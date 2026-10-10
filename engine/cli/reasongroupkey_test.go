package cli_test

import (
	"strings"
	"testing"
)

// Failures with one cause are counted as one cause.
//
// **Because the gate ranks its work list by group size, and the groups were
// wrong.** A corrupt store device made 146 of 179 targets fail with the same
// XFS error - and each carried its own temporary directory and its own layer
// hash, so they arrived as 146 groups of one. The list the gate exists to
// produce said the biggest problem was a missing Dockerfile, five times over,
// and the real one was invisible at the bottom.
//
// Normalising the volatile parts is what makes "the biggest group is the next
// thing worth fixing" true.
func TestOneCauseIsOneGroup(t *testing.T) {
	t.Parallel()

	// Two failures, one cause: different sandbox, different layer, same fault.
	a := "run 1c307f250243ea5fc036326c4f577e6232138ca489d5296847b7ca526f624f66:" +
		" open /tmp/TestHowManyEarthTestsBuild3686583200/004/tests/x:" +
		" structure needs cleaning"
	b := "run 4ad3181545f2e3e2e24ef030634508d03ed5560973c6e5c2d473276e10ebe820:" +
		" open /tmp/TestHowManyEarthTestsBuild2795912214/001/tests/y:" +
		" structure needs cleaning"

	if groupOf(a) != groupOf(b) {
		t.Errorf("two failures with one cause group apart:\n  %q\n  %q",
			groupOf(a), groupOf(b))
	}

	// And genuinely different causes stay apart.
	c := "run 1c307f250243ea5fc036326c4f577e6232138ca489d5296847b7ca526f624f66:" +
		" open /tmp/TestHowManyEarthTestsBuild3686583200/004/tests/x:" +
		" no such file or directory"

	if groupOf(a) == groupOf(c) {
		t.Errorf("two different causes group together as %q", groupOf(a))
	}

	// The group still says what happened: a key nobody can read is a key
	// nobody can act on.
	if !strings.Contains(groupOf(a), "structure needs cleaning") {
		t.Errorf("the group key %q does not name the fault", groupOf(a))
	}
}
