package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"
)

// TestAStepLongerThanTheReachStillCompletes.
//
// **The bound was on the work, and it was meant to be on the machine.** `ask`
// gives a worker `defaultReach` to answer and `askOver` sets that deadline on
// the stream it then reads the *result* off - so a step that takes longer than
// ten seconds is indistinguishable from a machine that has gone. Measured on a
// two-machine fleet: a worker fetching a 1032 MiB base was dropped mid-fetch,
// its store stayed empty, and the next assignment found it just as cold. Not a
// slow path but an absorbing state (E-F1).
//
// A busy worker says so. Liveness is then the gap between frames, and
// completion has no deadline of its own.
func TestAStepLongerThanTheReachStillCompletes(t *testing.T) {
	t.Parallel()

	const (
		beat  = 10 * time.Millisecond
		reach = 60 * time.Millisecond
		step  = 10 * beat
	)

	r, w := io.Pipe()

	go func() {
		_ = replyRunning(t.Context(), w, beat, func() (Reply, error) {
			time.Sleep(step)

			return Reply{Version: Version, Platform: "linux/amd64"}, nil
		})

		_ = w.Close()
	}()

	var extended int

	got, err := readReply(r, func(time.Time) { extended++ }, reach)
	if err != nil {
		t.Fatalf("a step that outlived the reach was read as a dead worker: %v", err)
	}

	if got.Platform != "linux/amd64" {
		t.Errorf("the reply says %q, want linux/amd64", got.Platform)
	}

	if extended < 2 {
		t.Errorf("the deadline was extended %d time(s), so a worker that went"+
			" quiet mid-step would not be noticed", extended)
	}
}

// TestAWorkerThatGoesQuietIsStillDropped. The other half: the bound has to
// still bite, or E256's corpse is back and costs a reach per step.
func TestAWorkerThatGoesQuietIsStillDropped(t *testing.T) {
	t.Parallel()

	r, w := io.Pipe()

	// One beat and then silence, which is a machine that died mid-step.
	go func() {
		_, _ = w.Write([]byte{noteAlive})

		select {} // a worker that never says anything again
	}()

	var mu sync.Mutex

	deadlines := 0

	_, err := readReply(r, func(time.Time) {
		mu.Lock()
		defer mu.Unlock()

		deadlines++

		if deadlines > 1 {
			_ = r.CloseWithError(context.DeadlineExceeded)
		}
	}, time.Millisecond)
	if err == nil {
		t.Error("a worker that stopped answering was waited on forever, which" +
			" is the corpse E256 removed from the fleet")
	}
}

// TestALegacyReplyIsStillRead. A worker built before this wrote a bare framed
// message, whose first byte is the top of an eight-byte length and therefore
// zero for anything this engine would send. Reading it as a tag would turn a
// version skew into a decode error nobody could place.
func TestALegacyReplyIsStillRead(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	body, err := json.Marshal(Reply{Version: Version, Platform: "linux/arm64"})
	if err != nil {
		t.Fatal(err)
	}

	err = WriteMessage(&buf, body)
	if err != nil {
		t.Fatal(err)
	}

	got, err := readReply(&buf, nil, time.Second)
	if err != nil {
		t.Fatalf("a reply from an older worker was unreadable: %v", err)
	}

	if got.Platform != "linux/arm64" {
		t.Errorf("the reply says %q, want linux/arm64", got.Platform)
	}
}
