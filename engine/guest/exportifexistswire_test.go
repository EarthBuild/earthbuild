package guest

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
)

// recordingGuest keeps what the client sent and answers each request emptily.
type recordingGuest struct {
	mu   sync.Mutex
	sent []byte // bytes not yet split into frames
	seen []byte // request bodies, whole
	out  chan []byte
	done chan struct{}
}

func (g *recordingGuest) Write(p []byte) (int, error) {
	g.mu.Lock()
	g.sent = append(g.sent, p...)

	// Frames are a four-byte big-endian length and then the body, written as
	// two calls, so the reply can only be built once a whole frame has arrived.
	for len(g.sent) >= 4 {
		n := int(binary.BigEndian.Uint32(g.sent[:4]))
		if len(g.sent) < 4+n {
			break
		}

		body := append([]byte(nil), g.sent[4:4+n]...)
		g.seen = append(g.seen, body...)
		g.sent = g.sent[4+n:]

		var req Request

		err := json.Unmarshal(body, &req)
		if err == nil {
			reply, _ := json.Marshal(Response{ID: req.ID})

			var hdr [4]byte

			binary.BigEndian.PutUint32(hdr[:], uint32(len(reply)))
			g.out <- append(hdr[:], reply...)
		}
	}

	g.mu.Unlock()

	return len(p), nil
}

func (g *recordingGuest) Read(p []byte) (int, error) {
	select {
	case b := <-g.out:
		return copy(p, b), nil
	case <-g.done:
		return 0, io.EOF
	}
}

func (g *recordingGuest) wrote() string {
	g.mu.Lock()
	defer g.mu.Unlock()

	return string(g.seen)
}

// The host tells the guest that an export may find nothing.
//
// `SAVE ARTIFACT --if-exists` is decided in the guest, because the materialised
// root is a path in the guest's mount namespace and the host cannot see it
// (E788). That only works if the host says so: the flag has to be on the wire.
//
// The guest's own half is covered by TestExportIfExistsSavesWhatIsThereAndSkips-
// WhatIsNot, which calls `s.export` directly - and therefore cannot notice a
// client that never sends the flag. The end-to-end test that would notice is
// gated on EARTH_TEST_NETWORK and a sandbox, so it does not run in an ordinary
// `go test ./...`.
//
// So the fix for E788 had its computing half tested and its wiring half not,
// which is the shape E794, E798 and E494 were all made of. The catalogue found
// it in my own work within the hour.
func TestTheExportRequestCarriesIfExists(t *testing.T) {
	t.Parallel()

	g := &recordingGuest{out: make(chan []byte, 4), done: make(chan struct{})}
	t.Cleanup(func() { close(g.done) })

	c := &Client{
		c:       newConn(g),
		pending: map[uint64]chan Response{},
		sinks:   map[uint64]func(string, bool){},
	}

	go c.read()

	h := &remoteHandle{c: c, id: "h-1", root: "/does/not/matter"}

	_, _, err := c.Export(context.Background(), h, "/a.txt", "out/a.txt", true)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	if !strings.Contains(g.wrote(), `"ifExists":true`) {
		t.Errorf("the export request does not carry ifExists:\n  %s"+
			"\n  the guest is the only side that can see whether the path is"+
			" there, and it is never told to look", g.wrote())
	}
}
