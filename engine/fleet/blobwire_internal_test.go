package fleet

import (
	"bytes"
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A malformed reply is refused as malformed, not as corruption.
//
// The two are different faults with different remedies. Corruption means the
// bytes were damaged getting here - retry, or ask somebody else. A root that is
// not a digest means the peer is not speaking this protocol at all, which no
// amount of retrying fixes and which a person needs to be told plainly.
//
// Without the length check the short root is zero-padded and the stream fails to
// verify against it, so the transfer still fails - correctly, and with a message
// that sends the reader looking at their network.
func TestARootThatIsNotADigestIsAProtocolFaultNotCorruption(t *testing.T) {
	t.Parallel()

	body := []byte("a blob worth carrying")
	stream, _ := EncodeBlob(body)

	var buf bytes.Buffer

	err := WriteMessage(&buf, []byte{1})
	if err != nil {
		t.Fatal(err)
	}

	// Five bytes where a digest belongs.
	err = WriteMessage(&buf, []byte("short"))
	if err != nil {
		t.Fatal(err)
	}

	err = WriteMessage(&buf, stream)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = readBlob(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("a reply with a five-byte root was accepted")
	}

	if !errors.Is(err, ErrMalformed) {
		t.Errorf("refused with %v, want ErrMalformed"+
			"\n  reported as corruption, this sends somebody to look at their"+
			" network for a peer that is speaking a different protocol", err)
	}
}

// A store that has a layer and cannot read it does not answer "absent".
//
// **The last hop of E312.** The driver's store said `Has` was true for exactly
// the layer a worker asked for, and the worker was told nobody held it. Between
// those two facts sits one call - `Get`, which packs the layer on demand - and
// its error was being dropped on the floor:
//
//	if err == nil { b = got }   // and b == nil means "I do not have it"
//
// So a store that holds a layer it cannot pack is indistinguishable on the wire
// from one that never had it. The remedies are opposite: an absence sends the
// caller to another peer, a broken store needs a person.
//
// *Failure class: a diagnostic discarded at each boundary it crosses.* This is
// the fourth boundary in this one path (E308, E309, E312).
func TestAStoreThatHasABlobAndCannotReadItSaysSo(t *testing.T) {
	t.Parallel()

	boom := errors.New("pack layer: permission denied")

	var out bytes.Buffer

	err := serveOneBlob(&out, &brokenHeld{err: boom}, ir.NodeID{7}, nil, false)
	if err == nil {
		t.Fatal("a store that could not read what it holds answered as though" +
			" it held nothing\n  five two-machine experiments went into" +
			" telling those two apart by hand (E312)")
	}

	if !errors.Is(err, boom) {
		t.Errorf("%v\n  does not carry the store's own reason", err)
	}
}

// brokenHeld holds everything and can read none of it.
type brokenHeld struct{ err error }

func (b *brokenHeld) Has(ir.NodeID) bool { return true }

func (b *brokenHeld) Get(ir.NodeID) ([]byte, error) { return nil, b.err }
