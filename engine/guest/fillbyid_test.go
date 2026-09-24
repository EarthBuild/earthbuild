package guest

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fillWire is a host that records what was asked and answers when told to.
type fillWire struct {
	mu   sync.Mutex
	sent bytes.Buffer
	in   chan []byte
}

func (w *fillWire) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.sent.Write(p)

	return len(p), nil
}

func (w *fillWire) Read(p []byte) (int, error) {
	b, ok := <-w.in
	if !ok {
		return 0, io.EOF
	}

	return copy(p, b), nil
}

func (w *fillWire) asked() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.sent.String()
}

// A fault-in answer goes to the request that asked for it, by id.
//
// A step opens files from several threads at once and the answers come back in
// whatever order the host produced them. Matching by arrival rather than by id
// would hand one fault-in another's verdict - and when one succeeded and one did
// not, that is a lie by another route (E291).
//
// The test uses one waiter and an answer addressed to nobody, because that is
// the case with a deterministic outcome: matching by id drops it and the caller
// stays waiting, while handing it to whoever is available unblocks a request
// that was never answered. Two waiters and a swapped pair would fail only when
// the arbitrary choice happened to be the wrong one.
func TestAFillAnswerGoesToTheRequestThatAsked(t *testing.T) {
	t.Parallel()

	w := &fillWire{in: make(chan []byte, 4)}
	f := NewFills(w)

	done := make(chan error, 1)

	go func() { done <- f.Fill("wanted.txt") }()

	// The request carries the id the answer has to name.
	var id uint64

	for range 100 {
		if s := w.asked(); strings.Contains(s, "wanted.txt") {
			var asked faultIn
			if json.Unmarshal([]byte(strings.TrimSpace(s)), &asked) == nil {
				id = asked.ID

				break
			}
		}

		time.Sleep(10 * time.Millisecond)
	}

	if id == 0 {
		t.Fatal("the fault-in was never put on the wire")
	}

	// Addressed to nobody. Nothing is waiting on this id.
	stray, _ := json.Marshal(filled{ID: id + 1000, Error: "for a request that does not exist"})
	w.in <- stray

	select {
	case err := <-done:
		t.Fatalf("the waiting request was answered by a reply addressed to"+
			" another id: %v\n  a step faulting in from several threads would"+
			" take its neighbour's verdict", err)
	case <-time.After(250 * time.Millisecond):
		// Still waiting, which is right.
	}

	// And its own answer does reach it, or the match is simply broken.
	own, _ := json.Marshal(filled{ID: id})
	w.in <- own

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the request's own answer arrived as an error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("the request's own answer never reached it")
	}
}
