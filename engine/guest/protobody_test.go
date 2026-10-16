package guest

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// A body that will not parse is quoted, because it names what wrote it.
//
// **The protocol is length-prefixed, so a parse failure means the stream is
// desynchronised.** Four bytes of length, then exactly that many bytes: there
// is no way to get invalid JSON out of a stream that is in step. Something has
// written bytes nobody framed, the reader has taken a length from the middle
// of a message, and every read after that is offset.
//
// "unmarshal: invalid character '/' after object key" therefore says only that
// it happened. The bytes themselves say what did it - a log line, a path, a
// greeting, whatever was written into a channel that carries frames - and they
// are already in hand when the error is made.
func TestABodyThatWillNotParseIsQuoted(t *testing.T) {
	t.Parallel()

	// A frame whose body is not JSON at all: the shape a desynchronised reader
	// sees when something wrote plain text into the channel.
	intruder := "earth-guestd: /var/lib/earthbuild is not writable\n"

	var buf bytes.Buffer

	var hdr [4]byte

	binary.BigEndian.PutUint32(hdr[:], uint32(len(intruder)))
	buf.Write(hdr[:])
	buf.WriteString(intruder)

	c := newConn(&buf)

	var resp Response

	err := c.recv(&resp)
	if err == nil {
		t.Fatal("a body that is not JSON was accepted")
	}

	if !strings.Contains(err.Error(), "earth-guestd") {
		t.Errorf("the error does not quote what was actually in the frame,"+
			" so it cannot say what wrote it:\n%s", err)
	}
}

// A long body is quoted in part, not in full.
//
// The point is to name the intruder, not to print a megabyte of layer into a
// terminal - and the first line of the wrong bytes is almost always the one
// that identifies them.
func TestAQuotedBodyIsBounded(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("x", 8192)

	var buf bytes.Buffer

	var hdr [4]byte

	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	buf.Write(hdr[:])
	buf.WriteString(body)

	c := newConn(&buf)

	var resp Response

	err := c.recv(&resp)
	if err == nil {
		t.Fatal("a body that is not JSON was accepted")
	}

	if len(err.Error()) > 1024 {
		t.Errorf("the error is %d bytes; a diagnostic nobody can read is not one", len(err.Error()))
	}
}

// A frame that goes wrong deep inside is quoted where it goes wrong.
//
// **The head of a spliced frame is healthy, which is why quoting the head says
// nothing.** The real ones look like this: a `reads` map of several thousand
// bytes whose first two hundred are a perfectly ordinary observation, and the
// damage somewhere in the middle. Printing the opening of the message confirms
// only that the reader was in step when the message began - which the length
// prefix already said.
//
// `json.SyntaxError` carries the byte offset it stopped at, so the window that
// contains the intruding bytes is known exactly rather than guessed at.
func TestAQuotedBodyIsWindowedOnTheFailure(t *testing.T) {
	t.Parallel()

	// A healthy observation, spliced: a line of somebody else's output dropped
	// into the middle of the map, exactly as a second writer to the channel
	// would leave it.
	head := `{"id":391,"reads":{` + strings.Repeat(`"/bin/`+strings.Repeat("a", 40)+`":"h",`, 60)
	splice := "earth-guestd: cannot open the store\n"
	body := head + splice + `"/bin/cat":"h"}}`

	var buf bytes.Buffer

	var hdr [4]byte

	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	buf.Write(hdr[:])
	buf.WriteString(body)

	c := newConn(&buf)

	var resp Response

	err := c.recv(&resp)
	if err == nil {
		t.Fatal("a body that is not JSON was accepted")
	}

	if !strings.Contains(err.Error(), "earth-guestd: cannot open the store") {
		t.Errorf("the error quotes the healthy opening of the frame rather than"+
			" the bytes it actually stopped on, which is the only part that"+
			" names what wrote them:\n%s", err)
	}

	if len(err.Error()) > 1024 {
		t.Errorf("the error is %d bytes; a diagnostic nobody can read is not one", len(err.Error()))
	}
}
