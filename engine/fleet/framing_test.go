package fleet_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// A stream is bytes and a message is not.
//
// Two messages and one longer message are the same bytes unless the boundary is
// written down - the same argument as a length-prefixed string one level in, for
// the same reason.
func TestTwoMessagesAreNotOneLongOne(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	for _, m := range [][]byte{[]byte("ab"), []byte("c"), {}, []byte("dddd")} {
		err := fleet.WriteMessage(&buf, m)
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, want := range []string{"ab", "c", "", "dddd"} {
		got, err := fleet.ReadMessage(&buf)
		if err != nil {
			t.Fatalf("reading %q: %v", want, err)
		}

		if string(got) != want {
			t.Errorf("read %q, want %q", got, want)
		}
	}

	if _, err := fleet.ReadMessage(&buf); !errors.Is(err, io.EOF) &&
		!errors.Is(err, fleet.ErrMalformed) {
		t.Errorf("reading past the end gave %v", err)
	}
}

// A length a peer invented is not an allocation this engine makes.
//
// The first four bytes of a control stream are a number chosen by somebody else.
// `make([]byte, n)` on it is a gigabyte allocated by a peer who sent four bytes,
// which is a denial of service with a one-line exploit - the same shape as the
// decoder's count bound (E245) and worth its own check because it is a different
// four bytes.
func TestALengthFromAPeerIsBounded(t *testing.T) {
	t.Parallel()

	// Eight bytes, because a blob's length has to reach past four (E280) and one
	// framing serves both. The hand-written frame tracks the format on purpose:
	// a test that constructed the frame through the writer could not send a
	// length the writer refuses to send.
	var wild [8]byte

	binary.BigEndian.PutUint64(wild[:], 1<<30)

	_, err := fleet.ReadMessage(bytes.NewReader(wild[:]))
	if !errors.Is(err, fleet.ErrMalformed) {
		t.Fatalf("a gigabyte length gave %v, want ErrMalformed", err)
	}

	// And for the bound rather than for running out of bytes, which is the
	// distinction E245 had to learn: a peer that also sent the gigabyte would
	// meet only the first.
	if !bytes.Contains([]byte(err.Error()), []byte("the most it will")) {
		t.Errorf("it was refused for running short, not for the length: %v", err)
	}
}

// A promise of more than arrives is refused rather than returned short.
func TestAShortMessageIsNotHalfAMessage(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	_ = fleet.WriteMessage(&buf, []byte("abcdef"))

	short := buf.Bytes()[:buf.Len()-2]

	_, err := fleet.ReadMessage(bytes.NewReader(short))
	if !errors.Is(err, fleet.ErrMalformed) {
		t.Errorf("a truncated message gave %v; half a message is not a"+
			" message", err)
	}
}

// A blob may be larger than a control message, because a layer is.
//
// One bound guarded both, at a megabyte - which is generous for an assignment
// and absurd for a layer. A 32 MiB layer packs to 33 MB, and the wire refused
// to send it: *"a message of 33685648 bytes, and 1048576 is the most this engine
// sends"*.
//
// **Blob transfer had therefore never carried a real layer**, and no test found
// it because every wire test used a layer small enough to fit through the hole
// meant for control messages (E280).
//
// Both bounds are still bounds. A length is a number the sender chose, and the
// answer to "how big may a layer be" is not "as big as it says".
func TestABlobMayBeLargerThanAControlMessage(t *testing.T) {
	t.Parallel()

	body := make([]byte, 4<<20) // four times the control bound
	for i := range body {
		body[i] = byte(i)
	}

	var buf bytes.Buffer

	err := fleet.WriteBlobMessage(&buf, body)
	if err != nil {
		t.Fatalf("a four-megabyte layer could not be written: %v", err)
	}

	got, err := fleet.ReadBlobMessage(&buf)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}

	if !bytes.Equal(got, body) {
		t.Errorf("read back %d bytes of %d", len(got), len(body))
	}
}

// A control message is still held to the smaller bound.
//
// The two bounds exist for different reasons. A blob is large because layers are
// large; an assignment that claims to be is a peer inventing a length, and
// nothing this engine sends on the control protocol is anywhere near a megabyte.
func TestAControlMessageIsStillHeldToItsOwnBound(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := fleet.WriteMessage(&buf, make([]byte, 4<<20))
	if err == nil {
		t.Error("a four-megabyte control message was sent")
	}
}
