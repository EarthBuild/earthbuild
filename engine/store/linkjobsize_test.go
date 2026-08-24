package store

import (
	"testing"
	"unsafe"
)

// A link job carries no padding it does not need.
//
// One of these exists per entry placed, and the flags were separated by the
// fields between them, so each was padded to a word: 72 bytes where 64 will do,
// which is also the difference between two allocator size classes (govet
// fieldalignment).
//
// Asserted rather than left to the linter, for the reason the layer's entry is:
// the order is load-bearing and nothing else says so.
func TestALinkJobHasNoPaddingToSpare(t *testing.T) {
	t.Parallel()

	const want = 64

	if got := unsafe.Sizeof(linkJob{}); got != want {
		t.Errorf("linkJob is %d bytes, want %d"+
			"\n  the bools must sit together, and there is one job per entry placed",
			got, want)
	}
}
