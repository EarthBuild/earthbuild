//go:build linux

package trace

import (
	"errors"
	"testing"
)

// TestACallThatNamesNoPathIsNotAnUnreadableOne.
//
// **`statx(fd, NULL, AT_EMPTY_PATH, …)` is how you stat a descriptor.** It is
// an ordinary, correct call that names no path: the file is the one the
// descriptor already refers to, and the pathname argument is a null pointer.
//
// Read as a string, a null pointer is address zero, nothing is mapped there,
// and `/proc/<pid>/mem` answers EIO. The tracer had no way to tell that from a
// path it genuinely could not read, so it declared the observation incomplete -
// and a step whose observation is incomplete can never earn an L2 hit again
// (I3).
//
// Which is most of what Rust does. `std::fs` metadata goes through `statx`, and
// cargo stats everything it can see, so `cargo install --root $CARGO_HOME`
// reported `a path argument that could not be read: input/output error` and
// lost the tier on every build. Measured on `examples/rust`: two observations
// lost per build, every build.
//
// Nothing is lost by dropping it. A descriptor was obtained by opening a path,
// and *that* call named it and was observed; stating the same file through the
// descriptor adds no path the cache does not already know.
func TestACallThatNamesNoPathIsNotAnUnreadableOne(t *testing.T) {
	t.Parallel()

	if !errors.Is(errNoPathNamed, errNoPathNamed) {
		t.Fatal("the sentinel does not match itself")
	}

	// A null pointer is the case; anything else is a path to be read.
	if !namesNoPath(0) {
		t.Error("a null path argument reads as a path, so statx of a descriptor" +
			" costs the step its observed-input tier")
	}

	if namesNoPath(0x7fff0000) {
		t.Error("an ordinary address reads as naming no path, so real reads" +
			" would go unobserved - which is the half that makes a wrong hit")
	}
}

// TestNamingNoPathIsDroppedRatherThanLost ties the sentinel to the decision, so
// that the two cannot drift apart: a call naming no path must not mark the
// observation incomplete.
func TestNamingNoPathIsDroppedRatherThanLost(t *testing.T) {
	t.Parallel()

	if got := whatToDoWith(errNoPathNamed, true); got != sightingDrop {
		t.Errorf("a call naming no path gave %v, wanted %v", got, sightingDrop)
	}

	if got := whatToDoWith(errors.New("a real failure"), true); got != sightingLose {
		t.Errorf("an unreadable path gave %v, wanted %v", got, sightingLose)
	}

	if got := whatToDoWith(nil, true); got != sightingRecord {
		t.Errorf("a path that was read gave %v, wanted %v", got, sightingRecord)
	}
}
