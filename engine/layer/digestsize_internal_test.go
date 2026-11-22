package layer

import (
	"bytes"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A digest is a hash and a size, and a reply that drops the size is not one.
//
// **Found by buck2, which counted.** FindMissingBlobs echoes the digests a
// client asked about; ours echoed the hash and wrote nothing for `size_bytes`,
// which proto3 omits when zero. A client matching replies against what it sent
// matches on the whole message, so every digest of a non-empty blob came back
// as one it had never asked about: buck2 requested twelve, recognised the one
// empty blob, and reported twenty-three.
//
// The golden vectors could not catch it. They are round trips through our own
// encoder, which dropped the size on the way in as well, so both halves agreed
// about a message neither had to justify to anyone.
func TestAMissingBlobReplyCarriesTheSizeItWasAsked(t *testing.T) {
	t.Parallel()

	want := []Blob{
		{ID: ir.DigestOf([]byte("one")), Size: 3},
		{ID: ir.DigestOf([]byte("")), Size: 0},
		{ID: ir.DigestOf([]byte("a longer blob")), Size: 13},
	}

	got, err := DigestsInRequest(EncodeFindMissingBlobs(want))
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != len(want) {
		t.Fatalf("sent %d digests and read back %d", len(want), len(got))
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("digest %d went out as %v and came back as %v", i, want[i], got[i])
		}
	}

	// And the reply, which is the half that was wrong: a client compares these
	// with what it sent, byte for byte.
	reply, err := DigestsInRequest(EncodeMissingBlobs(want))
	if err != nil {
		t.Fatal(err)
	}

	for i := range want {
		if reply[i].Size != want[i].Size {
			t.Errorf("%v was reported missing with size %d, and it was asked about"+
				" with size %d - a client matching on the digest it sent will not"+
				" recognise this one", want[i].ID, reply[i].Size, want[i].Size)
		}
	}

	// A request and its reply differ only in the field number, so the bytes of
	// one entry must be identical - which is what "echo" means here.
	if a, b := EncodeFindMissingBlobs(want)[1:], EncodeMissingBlobs(want)[1:]; !bytes.Equal(a, b) {
		t.Errorf("a request encodes as %x and its echo as %x", a, b)
	}
}
