package fleet

import (
	"context"
	"testing"
	"time"
)

// A stream carries the caller's deadline, not only the dial.
//
// The context covers opening the stream and nothing after it: the reads take no
// context, so a peer whose machine vanished after the stream was opened blocks
// until QUIC gives up - tens of seconds, once per step (E256). Setting the
// deadline on the stream is what actually applies it.
//
// Written because a mutant that removed the call survived a full sweep of 442.
// Nothing could see it: `bound` took the concrete stream, so there was no way
// to ask whether it had been told anything.
func TestAStreamIsGivenTheCallersDeadline(t *testing.T) {
	t.Parallel()

	want := time.Now().Add(90 * time.Second)

	ctx, cancel := context.WithDeadline(context.Background(), want)
	defer cancel()

	var got fakeStream

	bound(ctx, &got)

	if !got.set {
		t.Fatal("the stream was never given a deadline, so a peer that vanishes" +
			" blocks until QUIC gives up on the connection (E256)")
	}

	if !got.at.Equal(want) {
		t.Errorf("the stream's deadline is %v, not the caller's %v", got.at, want)
	}
}

// And a context with no deadline leaves the stream alone.
//
// The other half: a deadline invented here would cut off a transfer the caller
// never bounded, which is a build failing on a slow network rather than waiting.
func TestAStreamWithNoDeadlineIsLeftAlone(t *testing.T) {
	t.Parallel()

	var got fakeStream

	bound(context.Background(), &got)

	if got.set {
		t.Errorf("a deadline of %v was invented for an unbounded caller", got.at)
	}
}

type fakeStream struct {
	at  time.Time
	set bool
}

func (f *fakeStream) SetDeadline(t time.Time) error {
	f.at, f.set = t, true

	return nil
}
