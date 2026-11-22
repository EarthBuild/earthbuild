package fleet

import (
	"testing"
	"time"
)

// TestATallyIsReadByDifference. A worker's fillers outlive any one assignment,
// so a step charged the running total would be charged for the whole build.
func TestATallyIsReadByDifference(t *testing.T) {
	t.Parallel()

	var tally Tally

	tally.add(100, time.Second)

	was := tally.Moved()

	tally.add(40, 2*time.Second)

	got := tally.Since(was)
	if got.Bytes != 40 {
		t.Errorf("this step moved %d bytes, want 40", got.Bytes)
	}

	if got.Took != 2*time.Second {
		t.Errorf("this step spent %v fetching, want 2s", got.Took)
	}

	if tally.Fetches() != 2 {
		t.Errorf("%d fetch(es) recorded, want 2", tally.Fetches())
	}
}

// A nil tally is a worker nobody is counting, and must not panic.
func TestANilTallyCountsNothing(t *testing.T) {
	t.Parallel()

	var tally *Tally

	tally.add(100, time.Second)

	if got := tally.Moved(); got.Bytes != 0 {
		t.Errorf("a nil tally reports %d bytes", got.Bytes)
	}
}
