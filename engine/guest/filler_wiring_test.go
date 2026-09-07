package guest

import (
	"net"
	"testing"
)

// A guest with no fault-in channel fills nothing.
//
// Every build today. A nil filler leaves the tracer watching rather than
// filling, which is what it has always done - and the distinction matters
// because a tracer that *thinks* it can fault in will let a step proceed on a
// base that is not all there (E296).
func TestAGuestWithNoChannelFillsNothing(t *testing.T) {
	t.Parallel()

	s := &Server{}

	if s.filler("h1") != nil {
		t.Error("a guest with no fault-in channel offered to fill paths")
	}

	if got := s.placedIn("h1", "/w"); len(got) != 0 {
		t.Errorf("it also claimed to have placed %v", got)
	}
}

// A guest with a channel fills through it.
func TestAGuestWithAChannelFillsThroughIt(t *testing.T) {
	t.Parallel()

	here, there := net.Pipe()

	t.Cleanup(func() { _ = there.Close() })

	s := &Server{Fills: NewFills(here)}

	if s.filler("h1") == nil {
		t.Error("a guest with a fault-in channel offered no filler")
	}
}
