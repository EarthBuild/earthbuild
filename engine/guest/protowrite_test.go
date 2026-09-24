package guest

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

// sizedWriter records how much it was asked to write each time.
type sizedWriter struct {
	bytes.Buffer

	calls []int
}

func (w *sizedWriter) Write(p []byte) (int, error) {
	w.calls = append(w.calls, len(p))

	return w.Buffer.Write(p) //nolint:wrapcheck // a test double
}

func (w *sizedWriter) largest() int {
	most := 0
	for _, n := range w.calls {
		if n > most {
			most = n
		}
	}

	return most
}

// No single write to a guest connection exceeds what the transport carries
// intact.
//
// **Firecracker replays 32 KiB of a write larger than this whenever the reader
// stalls.** Measured against firecracker v1.13.1 with a standalone probe: a
// guest writing a counting stream to a host that pauses 5 ms every MiB sees the
// stream jump backwards by exactly 32768 bytes, once per write, for every write
// size above 32768 - and never once at 32768 or below. The threshold is half of
// the VMM's 64 KiB per-connection TX ring (CONN_TX_BUF_SIZE), and the fault is
// silent: the bytes arrive, they are simply the wrong ones.
//
// The engine noticed because a step's `reads` observation runs to a megabyte,
// and a duplicated 32 KiB inside a length-prefixed frame desynchronises the
// stream for good - "guest connection lost", a megabyte after the damage.
//
// Chunking here rather than in the guest's writer because both ends send
// through this one function, and the host's requests cross the same device.
func TestNoWriteToTheConnectionExceedsWhatTheTransportCarries(t *testing.T) {
	t.Parallel()

	w := &sizedWriter{}
	c := newConn(w)

	// Comfortably past the ring, and past any single-write threshold: a real
	// observation of a Go build is this size.
	err := c.send(Response{ID: 1, Chunk: strings.Repeat("x", 900<<10)})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if got := w.largest(); got > vsockWrite {
		t.Errorf("a single write of %d bytes exceeds the %d the transport"+
			" carries intact; firecracker replays 32 KiB of it", got, vsockWrite)
	}
}

// Chunking changes how the bytes leave, not what they say.
func TestAChunkedFrameIsStillOneFrame(t *testing.T) {
	t.Parallel()

	w := &sizedWriter{}
	c := newConn(w)

	body := strings.Repeat("y", 200<<10)

	err := c.send(Response{ID: 9, Chunk: body})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	raw := w.Bytes()
	n := binary.BigEndian.Uint32(raw[:4])

	if int(n) != len(raw)-4 {
		t.Fatalf("the frame declares %d bytes and carries %d", n, len(raw)-4)
	}

	var back Response

	err = json.Unmarshal(raw[4:], &back)
	if err != nil {
		t.Fatalf("the reassembled frame does not parse: %v", err)
	}

	if back.ID != 9 || back.Chunk != body {
		t.Errorf("the frame came back changed: id %d, %d bytes of chunk",
			back.ID, len(back.Chunk))
	}
}
