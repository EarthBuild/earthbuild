package guest

import (
	"bytes"
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
