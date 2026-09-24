package fleet

import (
	"bytes"
	"io"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// bigHeld holds one large layer and cannot fragment.
type bigHeld struct{ bytes int }

func (bigHeld) Has(ir.NodeID) bool { return true }

func (b bigHeld) Get(ir.NodeID) ([]byte, error) { return make([]byte, b.bytes), nil }

// TestAFragmentRequestIsNotAnsweredWithAWholeLayer.
//
// **The answer nobody can accept, committed to a stream nobody will drain.**
// `serveOneBlob` falls through to the whole-blob path when the store cannot
// fragment, and `readFragment` refuses anything that is not a fragment - by
// construction, because answering "here is the whole layer" to "give me these
// paths" would be I10's accepted-and-ignored.
//
// So the sender writes a layer the asker has already decided to refuse, the
// asker returns after one flag, and the rest sits in the stream. Enough of
// those and the connection's flow-control window is gone: the driver cannot
// send even the first byte of the *next* answer, and the worker waits for it
// for ever. Both ends blocked on the same transfer, which is what the two
// goroutine dumps showed - `writeFramed` on one side, `readFragment` on the
// other (E-F2).
//
// A driver whose store is inside the VM can never fragment, so this is not an
// edge: it is every lazy fetch from a Mac.
func TestAFragmentRequestIsNotAnsweredWithAWholeLayer(t *testing.T) {
	t.Parallel()

	var out pipeBuffer

	// Asked for two paths, by a store that has the layer whole and cannot cut
	// it up.
	err := serveOneBlob(&out, bigHeld{bytes: 4 << 20}, ir.NodeID{1},
		[]string{"etc/hosts", "bin/sh"}, false)
	if err != nil {
		t.Fatalf("serving: %v", err)
	}

	// One byte of flag and its framing, and nothing else. A megabyte here is a
	// megabyte the asker will not read.
	if len(out.b) > 64 {
		t.Errorf("answered a fragment request with %d bytes; the asker refuses"+
			" anything that is not a fragment, so every one of them sits in the"+
			" stream", len(out.b))
	}

	// And it reads as "not here", which sends the asker to the next source and
	// then to the whole-layer path (I11).
	_, _, err = readFragment(bytesOf(out.b), ir.NodeID{1})
	if err == nil {
		t.Error("the asker read a fragment out of an answer that had none")
	}
}

// TestAWholeBlobRequestIsStillAnsweredWhole. The fallback is only wrong when
// paths were named: a request for the layer itself must still get the layer.
func TestAWholeBlobRequestIsStillAnsweredWhole(t *testing.T) {
	t.Parallel()

	var out pipeBuffer

	err := serveOneBlob(&out, bigHeld{bytes: 1 << 20}, ir.NodeID{1}, nil, false)
	if err != nil {
		t.Fatalf("serving: %v", err)
	}

	if len(out.b) < 1<<20 {
		t.Errorf("a request for the whole layer was answered with %d bytes", len(out.b))
	}
}

// bytesOf reads back what was written.
func bytesOf(b []byte) io.Reader { return bytes.NewReader(b) }
