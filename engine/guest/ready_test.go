package guest

import (
	"context"
	"net"
	"testing"
	"time"
)

// The handshake is answered before the agent is ready to work.
//
// **Housekeeping must not sit on the handshake path.** The agent collects its
// store before serving, and on a device-backed store that collection cannot be
// skipped: `earth prune` collects the host's directory and never reaches a
// microVM's image. But the host waits only thirty seconds for a handshake, so
// an unbudgeted collection is killed every time - which is exactly what
// happened when one was attempted on a store with 7M free: the guest said
// "starting, collecting the store", the host gave up, and nothing was ever
// collected.
//
// Answering Hello immediately and making real work wait resolves both: the host
// is satisfied, the collection runs to completion, and the first step waits for
// it rather than the connection dying under it.
func TestHelloIsAnsweredBeforeTheAgentIsReady(t *testing.T) {
	t.Parallel()

	ready := make(chan struct{})
	host, guestSide := net.Pipe()

	s := &Server{Ready: ready}

	go func() { _ = s.Serve(context.Background(), guestSide) }()

	c := newConn(host)

	err := c.send(Request{Kind: KindHello, Version: Version})
	if err != nil {
		t.Fatal(err)
	}

	// Answered while the agent is still busy, which is the whole point.
	got := make(chan Response, 1)

	go func() {
		var resp Response
		if err := c.recv(&resp); err == nil {
			got <- resp
		}
	}()

	select {
	case resp := <-got:
		if resp.Err != "" {
			t.Fatalf("the handshake was refused: %s", resp.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handshake went unanswered while the agent was collecting," +
			" which is the timeout this exists to prevent")
	}

	close(ready)
}
