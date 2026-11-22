package fleet_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// A liar is caught within a chunk, not at the end of the transfer.
//
// C.4's requirement, and the reason a whole-blob hash is not enough: hashing at
// the end detects corruption after the entire layer has arrived, so a peer
// serving rubbish costs a gigabyte of somebody's bandwidth before anything
// notices. Verified streaming costs one group.
//
// The assertion is on **how much got through**, because that is the property.
// A test that only checked the error would pass against a whole-blob hash, and
// the whole point of this file is that a whole-blob hash is not sufficient.
func TestALiarIsCaughtWithinAChunkAndNotAtTheEnd(t *testing.T) {
	t.Parallel()

	// A megabyte, so "detected early" and "detected at the end" are far apart.
	data := bytes.Repeat([]byte("earthbuild"), 100_000)

	stream, id := fleet.EncodeBlob(data)

	// Corrupt one byte near the beginning of the payload. Past the tree's
	// header, so what is being tested is a bad *chunk* rather than a malformed
	// stream.
	bad := append([]byte(nil), stream...)
	bad[len(bad)/50] ^= 0xff

	var got bytes.Buffer

	err := fleet.VerifiedCopy(&got, bytes.NewReader(bad), id)
	if !errors.Is(err, fleet.ErrCorrupt) {
		t.Fatalf("corruption was accepted: %v", err)
	}

	// The number is generous on purpose: what matters is that it is a fraction
	// of the blob rather than all of it. A whole-blob hash would have written
	// every byte before deciding.
	if got.Len() >= len(data)/2 {
		t.Errorf("%d of %d bytes were written before the corruption was caught;"+
			" a peer serving rubbish should cost a chunk, not a layer",
			got.Len(), len(data))
	}

	t.Logf("caught after %d of %d bytes", got.Len(), len(data))
}

// An honest stream arrives intact.
func TestAVerifiedCopyDeliversWhatWasSent(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 1, 1024, 70000} {
		data := bytes.Repeat([]byte("x"), size)

		stream, id := fleet.EncodeBlob(data)

		var got bytes.Buffer

		err := fleet.VerifiedCopy(&got, bytes.NewReader(stream), id)
		if err != nil {
			t.Fatalf("%d bytes: %v", size, err)
		}

		if !bytes.Equal(got.Bytes(), data) {
			t.Errorf("%d bytes: the copy differs from the original", size)
		}
	}
}

// A stream verified against somebody else's digest is refused.
//
// The case a fetch actually faces: a peer answering with a real blob that is not
// the one that was asked for. It hashes perfectly - to something else.
func TestAStreamForADifferentBlobIsRefused(t *testing.T) {
	t.Parallel()

	stream, _ := fleet.EncodeBlob([]byte("what the peer has"))
	_, wanted := fleet.EncodeBlob([]byte("what was asked for"))

	var got bytes.Buffer

	err := fleet.VerifiedCopy(&got, bytes.NewReader(stream), wanted)
	if !errors.Is(err, fleet.ErrCorrupt) {
		t.Errorf("a peer's substitution was accepted: %v", err)
	}
}
