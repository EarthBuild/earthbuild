package fleet

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A worker says what it is when it joins, before it has run anything.
//
// Placement refuses a worker that has not declared a platform, and a worker
// declared one by echoing the platform of an assignment it had run - so it could
// not be given a first step until it had run a first step. **A fresh worker
// could never be given work**, and the fleet had therefore never delegated
// anything on a build whose steps name a platform, which is every build (E503).
//
// The answer travels the direction the protocol already has: the driver opens a
// stream and the worker answers on it, which is what `answer` already does for
// an assignment and for a blob request. One more kind, and no new direction.
func TestAWorkerAnswersWhatItIs(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	buf.WriteByte(kindHello)

	// A `Reply` already carries all three, and B.1 says there is one encoding:
	// a hello with a type of its own would be a second way to say the same
	// thing.
	self := Reply{
		Version:  Version,
		Platform: "linux/arm64",
		Capacity: 4,
		HeldAt:   "abc@127.0.0.1:5000",
	}

	out := &recordingStream{in: &buf}

	answer(t.Context(), out, nil, nil, self, func(error) {})

	body, err := ReadMessage(bytes.NewReader(out.written.Bytes()))
	if err != nil {
		t.Fatalf("reading what the worker said: %v", err)
	}

	var got Reply

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding what the worker said: %v", err)
	}

	if got.Platform != self.Platform {
		t.Errorf("the worker says it runs %q, want %q", got.Platform, self.Platform)
	}

	if got.Capacity != self.Capacity {
		t.Errorf("the worker says it has room for %d, want %d", got.Capacity, self.Capacity)
	}

	if got.HeldAt != self.HeldAt {
		t.Errorf("the worker serves layers at %q, want %q", got.HeldAt, self.HeldAt)
	}
}

// And the driver records it, so the worker can be placed on at once.
//
// The inventory is what placement reads. A worker in it with no platform is a
// worker nothing can be given.
func TestAnAnnouncedWorkerIsPlaceable(t *testing.T) {
	t.Parallel()

	r := &Rendezvous{}
	r.AddForTest()

	before := r.Inventory()
	if len(before) != 1 {
		t.Fatalf("the fleet has %d workers", len(before))
	}

	if before[0].Platform != (ir.Platform{}) {
		t.Fatal("a worker that has said nothing already has a platform")
	}

	r.note(before[0].ID, "", "linux/arm64", 4)

	after := r.Inventory()
	if after[0].Platform == (ir.Platform{}) {
		t.Error("a worker that announced its platform is still in the" +
			" inventory without one, so placement will never give it a step")
	}
}

// recordingStream is a stream whose input is scripted and whose output is kept.
type recordingStream struct {
	in      io.Reader
	written bytes.Buffer
}

func (s *recordingStream) Read(p []byte) (int, error)  { return s.in.Read(p) }
func (s *recordingStream) Write(p []byte) (int, error) { return s.written.Write(p) }
func (s *recordingStream) Close() error                { return nil }
