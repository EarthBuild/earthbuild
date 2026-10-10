package guest

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestTheHostGoingAwayAbandonsWhatItAskedFor.
//
// **A step outlives the build that asked for it.** `Serve` returned on `io.EOF`
// without cancelling anything, so a host that dies - or is killed, which is what
// the corpus harness does at its timeout - left the guest's steps running. Any
// of them already blocked in the kernel waiting for the syscall tracer stayed
// blocked for ever, and because the sandbox is reused they poisoned every later
// build: one timeout in a sweep tends to be followed by others.
//
// Best effort, like `cancel`: a request that finished a moment ago is not an
// error to abandon.
func TestTheHostGoingAwayAbandonsWhatItAskedFor(t *testing.T) {
	t.Parallel()

	s := &Server{}

	first, cancelFirst := context.WithCancel(context.Background())
	second, cancelSecond := context.WithCancel(context.Background())

	defer cancelFirst()
	defer cancelSecond()

	s.began(1, cancelFirst)
	s.began(2, cancelSecond)

	s.abandonAll()

	for i, ctx := range []context.Context{first, second} {
		select {
		case <-ctx.Done():
		default:
			t.Errorf("request %d was left running after the host went away", i+1)
		}
	}

	// And nothing is left to abandon twice.
	s.mu.Lock()
	left := len(s.running)
	s.mu.Unlock()

	if left != 0 {
		t.Errorf("%d requests are still recorded as running", left)
	}

	// A server that never served anything abandons nothing and does not panic.
	(&Server{}).abandonAll()
}

// And Serve does it: the helper being right is no use if nothing calls it.
//
// A real connection, closed under a running Serve, because the wiring is the
// half that was missing - `Serve` returned on EOF and abandoned nothing.
func TestServeAbandonsWhenTheConnectionCloses(t *testing.T) {
	t.Parallel()

	host, guestSide := net.Pipe()

	s := &Server{}

	served := make(chan struct{})

	go func() {
		defer close(served)

		_ = s.Serve(context.Background(), guestSide)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.began(1, cancel)

	// The host goes away, which is what a killed build looks like from here.
	_ = host.Close()

	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return when the connection closed")
	}

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Error("Serve returned without abandoning the work the host asked for")
	}
}
