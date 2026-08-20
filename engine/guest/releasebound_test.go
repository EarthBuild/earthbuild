package guest

import (
	"io"
	"strings"
	"testing"
	"time"
)

// deafGuest accepts every request and answers none.
//
// Not a dead connection - that case is handled, and `read` wakes every waiting
// caller when the socket ends. This is the other one: a guest that is alive,
// holding the connection open, and not replying.
type deafGuest struct{ closed chan struct{} }

func (g *deafGuest) Write(p []byte) (int, error) { return len(p), nil }

func (g *deafGuest) Read([]byte) (int, error) {
	<-g.closed

	return 0, io.EOF
}

// Releasing a handle gives up eventually.
//
// `Release` runs from a cleanup, after the caller's context is gone, so it uses
// a context of its own - and used `context.Background()`, which is not a context
// of its own but *no bound at all*. A guest that stopped answering therefore
// stopped the build for ever, in a deferred call during teardown, where nothing
// is left to interrupt it.
//
// Found in a goroutine dump after the execution gate sat on one target for
// thirteen minutes under a sixty-second deadline (E442). **Not the caller's
// context is a reason to make a new one, not a reason to have none.**
func TestReleasingAHandleGivesUpEventually(t *testing.T) {
	t.Parallel()

	g := &deafGuest{closed: make(chan struct{})}
	t.Cleanup(func() { close(g.closed) })

	// Built directly rather than through Dial: this guest never answers the
	// handshake either, and that wait is bounded by its own test below.
	c := &Client{c: newConn(g), pending: map[uint64]chan Response{}, sinks: map[uint64]func(string){}}

	go c.read()

	// Constructed directly: what is under test is the wait, and going through
	// Materialise would need a guest that answers - which is the one thing this
	// one does not do.
	h := &remoteHandle{c: c, id: "h1"}

	done := make(chan error, 1)

	go func() { done <- h.Release() }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("releasing a handle nobody answered for reported success")

			return
		}

		if !strings.Contains(err.Error(), "release") {
			t.Errorf("gave up with %q, which does not say what was being done", err)
		}

	case <-time.After(90 * time.Second):
		t.Error("releasing a handle waited for a guest that never answers" +
			"\n  a cleanup with no deadline is a build nothing can stop")
	}
}

// The handshake gives up too.
//
// A guest that connects and never greets was a build that never started and
// could not be interrupted: `Dial` read the reply with no bound at all. The same
// failure as the release, one step earlier and before there is anything for the
// caller to cancel (E442).
func TestTheHandshakeGivesUpEventually(t *testing.T) {
	t.Parallel()

	g := &deafGuest{closed: make(chan struct{})}
	t.Cleanup(func() { close(g.closed) })

	done := make(chan error, 1)

	go func() {
		_, err := Dial(g)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Dial succeeded against a guest that said nothing")
		}

		if !strings.Contains(err.Error(), "handshake") {
			t.Errorf("gave up with %q, which does not say what was being waited for", err)
		}

	case <-time.After(greetingAtMost + 30*time.Second):
		t.Error("Dial waited for a greeting that never came")
	}
}
