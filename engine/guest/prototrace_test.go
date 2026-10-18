package guest

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// The received stream can be captured verbatim, because a desynchronised one
// cannot be diagnosed from the frame that reports it.
//
// **The frame that fails to parse is not the frame that went wrong.** A reader
// that has taken a length from the middle of a message reads a window into that
// message, and every frame after it is a window into the next: by the time the
// JSON is invalid the boundary that moved is megabytes behind. The only thing
// that names it is the byte stream itself, replayed from the start against the
// framing rules.
func TestTheReceivedStreamCanBeCaptured(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvProtoTrace, dir)

	body := `{"id":7}`

	var buf bytes.Buffer

	var hdr [4]byte

	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	buf.Write(hdr[:])
	buf.WriteString(body)

	c := newConn(&buf)

	var resp Response

	err := c.recv(&resp)
	if err != nil {
		t.Fatalf("a well-formed frame was refused: %v", err)
	}

	found, err := filepath.Glob(filepath.Join(dir, "*.frames"))
	if err != nil || len(found) != 1 {
		t.Fatalf("the capture wrote %d files, want 1 (%v)", len(found), err)
	}

	got, err := os.ReadFile(found[0])
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, append(hdr[:], body...)) {
		t.Errorf("the capture holds %q, which is not what was read;"+
			" a capture that is not byte-exact cannot locate a boundary", got)
	}
}

// Capture is off unless asked for, because it writes every byte of every build.
func TestTheStreamIsNotCapturedByDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvProtoTrace, "")

	var buf bytes.Buffer

	newConn(&buf)

	found, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(found) != 0 {
		t.Errorf("capture wrote %d files with %s unset", len(found), EnvProtoTrace)
	}
}
