package fleet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// wedged is a stream that accepts a request and then never reads a byte of the
// answer - a peer that asked, stopped, and did not close.
type wedged struct {
	req      io.Reader
	deadline time.Time
}

func (w *wedged) Read(p []byte) (int, error) { return w.req.Read(p) }

func (w *wedged) Write([]byte) (int, error) {
	// A send buffer that is already full: nothing moves and nothing fails, and
	// the only thing that ever ends it is a deadline.
	for {
		if !w.deadline.IsZero() && !time.Now().Before(w.deadline) {
			return 0, errors.New("i/o timeout")
		}

		time.Sleep(2 * time.Millisecond)
	}
}

func (w *wedged) Close() error { return nil }

func (w *wedged) SetDeadline(t time.Time) error {
	w.deadline = t

	return nil
}

// TestServingABlobCannotWedgeForever.
//
// **A driver serves the base of every build, and could be stopped by one
// peer.** `serveBlobStream` took a context and discarded it - the signature
// said `_ context.Context` - and set no deadline, so a write to a client that
// had stopped reading blocked in `writeFramed` with no way out.
//
// Found on an eight-step chain across two machines: three goroutines stuck
// writing 0x2828288 bytes each, which is one 40 MB layer apiece, and a build
// that reported no progress for six minutes. Nothing in the fleet times a
// serve out, and QUIC will wait as long as the peer keeps the connection.
//
// The bound has to be the serve's own. `fleet.Driver` serves under a context
// with a cancel and no deadline, so taking one from the context sets nothing at
// all - which is what the first attempt at this did, and it passed a test whose
// context happened to have one.
func TestServingABlobCannotWedgeForever(t *testing.T) {
	// Not parallel: it sets the bound it asserts against.

	// One blob asked for, and a store that has it.
	buf := &pipeBuffer{}

	err := writeRequest(buf, []ir.NodeID{{1}}, nil, false)
	if err != nil {
		t.Fatal(err)
	}

	st := &wedged{req: newBytesReader(buf.b)}

	// **A context with no deadline, because that is the one production has.**
	// `fleet.Driver` serves under `context.WithCancel(context.WithoutCancel(
	// ctx))`, which carries a cancel and no deadline - so a bound taken from
	// the context sets nothing, and an earlier attempt at this fixed the test
	// and not the engine.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// A bound short enough to assert against; production's is five minutes.
	t.Setenv(EnvServeWait, "150ms")

	done := make(chan struct{})

	go func() {
		defer close(done)

		serveBlobStream(ctx, st, &fakeHeld{}, func(error) {})
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("serving a blob to a peer that stopped reading never returned," +
			" so one client can wedge the machine that serves every build")
	}
}

// fakeHeld holds one blob of a size worth blocking on.
type fakeHeld struct{}

func (fakeHeld) Has(ir.NodeID) bool { return true }

func (fakeHeld) Get(ir.NodeID) ([]byte, error) { return make([]byte, 1<<20), nil }

// newBytesReader is bytes.NewReader, named apart so the import stays local to
// this file's intent.
func newBytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
