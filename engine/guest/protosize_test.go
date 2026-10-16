package guest

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// A message too big to be read is refused where it is written.
//
// **The limit was on one side only.** `recv` refuses anything over 16 MiB and
// `send` had no opinion, so an oversized message was written in full, the reader
// gave up on the frame, and the connection died. What the caller saw was
//
//	materialise the filesystem holding /earthly/go.mod:
//	  guest connection lost: message of 19580676 bytes exceeds the limit
//
// which names neither what was being sent nor by whom - and blames the request
// that happened to be next through the door rather than the one that was too
// big (E617). This repository's own `+unit-test` hits it on linux.
//
// Refusing at the sender keeps the connection usable and puts the size next to
// the thing that has it.
func TestAMessageTooBigToReadIsRefusedWhereItIsWritten(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	c := newConn(&rw{w: &buf})

	err := c.send(Request{Kind: KindMaterialise, Path: strings.Repeat("x", maxMessage+1)})
	if err == nil {
		t.Fatal("a message larger than the reader's limit was written")
	}

	for _, want := range []string{"materialise", "16"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal never mentions %q: %v", want, err)
		}
	}

	// **Nothing was written**, which is the half that matters: a partial frame
	// leaves the reader mid-message and every later request is misread as its
	// continuation. That is why the symptom was a lost connection rather than
	// one failed call.
	if buf.Len() != 0 {
		t.Errorf("%d bytes of an unreadable frame reached the wire", buf.Len())
	}
}

// An ordinary message is unaffected, or the guard is a ceiling on the protocol
// rather than on what breaks it.
func TestAnOrdinaryMessageIsStillSent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	c := newConn(&rw{w: &buf})

	err := c.send(Request{Kind: KindMaterialise, Path: "/earthly/go.mod"})
	if err != nil {
		t.Fatalf("an ordinary request was refused: %v", err)
	}

	if buf.Len() == 0 {
		t.Error("an accepted message wrote nothing")
	}
}

// rw is a writer that is also a (never-read) reader, since newConn wants both.
type rw struct{ w *bytes.Buffer }

func (x *rw) Read([]byte) (int, error)    { return 0, nil }
func (x *rw) Write(p []byte) (int, error) { return x.w.Write(p) }

// A reply too large to send is still answered.
//
// **The guard turned a broken connection into a hung build.** Before it, an
// oversized reply was written, the reader gave up on the frame and the caller
// saw a lost connection. After it, the write was refused, `_ = c.send(resp)`
// dropped the error - "a send failure means the connection is gone", which had
// been true - and the caller waited for ever for a message nobody would ever
// write. Observed: `+unit-test` on linux, stuck with no output for nineteen
// minutes where it used to fail in seven (E617).
//
// So the refusal has to arrive. The connection is healthy; it is this reply that
// is impossible, and the caller can be told so in a frame that fits.
func TestAReplyTooLargeToSendIsStillAnswered(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	c := newConn(&rw{w: &buf})

	huge := Response{ID: 7, Root: strings.Repeat("x", maxMessage+1)}

	err := reply(c, KindObserve, huge)
	if err != nil {
		t.Fatalf("answering with the reason failed: %v", err)
	}

	if buf.Len() == 0 {
		t.Fatal("nothing was written, so the caller is still waiting")
	}

	if buf.Len() > maxMessage {
		t.Errorf("the answer was itself %d bytes, which cannot be read either", buf.Len())
	}

	// The frame that did go out has to name the request it answers, or the
	// caller cannot match it to what it asked and waits anyway.
	if !bytes.Contains(buf.Bytes(), []byte(`"id":7`)) {
		t.Error("the refusal does not name the request it answers")
	}

	if !bytes.Contains(buf.Bytes(), []byte("exceeds")) {
		t.Error("the refusal does not say why")
	}

	// And which request it answers. A response carries a size and not a
	// subject, so without this the refusal says only "response" - which is how
	// an oversized frame survived three rounds of diagnosis unnamed (E618).
	if !bytes.Contains(buf.Bytes(), []byte("observe")) {
		t.Error("the refusal does not name the request it answers")
	}
}

// A step whose output was streamed does not send it a second time.
//
// **The 18.7 MiB message, found.** `Response.Output` carries a step's whole
// combined output, and a streamed step has already delivered every byte of it as
// chunks - so the reply repeats it. For this repository's `+unit-test` that is
// about nineteen megabytes, which is over the frame limit, which is why the build
// died on linux and then hung (E617).
//
// It was not merely oversized, it was **unread**: `core.StepError` prints the
// output only `if !e.Streamed`, so the second copy is transferred and discarded.
// Not sending it removes a multi-megabyte round trip per streamed step as well as
// the failure.
//
// A step the host did not ask to stream still gets its output, because nothing
// else delivered it.
func TestAStreamedStepDoesNotSendItsOutputTwice(t *testing.T) {
	t.Parallel()

	const out = "the step said this"

	if got := outputFor(Request{Stream: true}, []byte(out)); got != "" {
		t.Errorf("a streamed step repeated %d bytes of output in its reply: %q", len(got), got)
	}

	if got := outputFor(Request{}, []byte(out)); got != out {
		t.Errorf("an unstreamed step lost its output: %q, want %q", got, out)
	}
}

// An observation that could not be fetched says why.
//
// **The reason was thrown away.** `Observations` discarded the error and
// returned an empty observation, so a step whose observation was too large to
// send - about 16 MB is the boundary, and this repository's `+unit-test` reaches
// it (E618, E620) - was reported to the operator as
//
//	1 not observed (Earthfile:7: nothing observed this step)
//
// which is false. Something *was* watching, and it saw a great deal; what failed
// was delivering it. I11 asks the engine to degrade and to say so, and it was
// doing the first half.
//
// Marked incomplete as well as explained: an empty observation presented as fact
// agrees with every base in existence, which is the false hit I3 forbids.
func TestAnObservationThatCouldNotBeFetchedSaysWhy(t *testing.T) {
	t.Parallel()

	obs := unfetchedObservation(errors.New("the reply to observe could not be sent: too big"))

	if !obs.Incomplete {
		t.Error("an observation that never arrived was presented as complete")
	}

	if len(obs.Why) == 0 {
		t.Fatal("no reason was recorded, so the operator is told nothing was watching")
	}

	if !strings.Contains(obs.Why[0], "observe") {
		t.Errorf("the reason does not name what failed: %q", obs.Why[0])
	}

	// Empty rather than nil, because the caller ranges over them.
	if obs.Reads == nil || obs.Listings == nil {
		t.Error("the maps are nil, which the caller does not expect")
	}
}
