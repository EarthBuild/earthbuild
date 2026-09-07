package fleet

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A proof crosses compressed.
//
// **2.6x the fragment it authenticates** (E339), and it is the most compressible
// thing this engine sends: a few thousand entries of nearly-identical structure,
// with paths sharing prefixes and mode, ownership and device bytes repeating
// exactly. It crosses once per worker per layer, so a fleet of ten pays for ten
// copies of the same highly regular bytes.
//
// This does not remove the O(n) - only a Merkle identity does, and that is a
// change to what a layer *is* (§3.2, E339). It removes the constant, which is
// large, and it is a change to one message rather than to the cache.
//
// **The manifest only.** A fragment's payload is file contents, which are
// already whatever they are; spending processor time compressing an archive of
// compressed files is how a transfer gets slower.
func TestAProofCrossesCompressed(t *testing.T) {
	t.Parallel()

	// A manifest's shape: many entries differing in little.
	var proof bytes.Buffer

	for i := range 2000 {
		fmt.Fprintf(&proof, "usr/lib/lib%d.so", i)
		proof.Write(make([]byte, 60))
	}

	body := []byte(strings.Repeat("x", 4096))

	var out bytes.Buffer

	err := writeFragment(&out, proof.Bytes(), body)
	if err != nil {
		t.Fatalf("%v", err)
	}

	// **Measured before reading.** `readFragment` consumes the buffer, so asking
	// its length afterwards asks how much is left, which is none - and the test
	// passed against an uncompressed wire until this line moved.
	sent := out.Len()

	// Round trip: a smaller proof that does not arrive is not a saving.
	gotProof, gotBody, err := readFragment(&out, ir.NodeID{1})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if !bytes.Equal(gotProof, proof.Bytes()) {
		t.Fatal("the proof did not survive the wire")
	}

	if !bytes.Equal(gotBody, body) {
		t.Fatal("the fragment did not survive the wire")
	}

	t.Logf("a %d-byte proof crossed as %d bytes with a %d-byte fragment",
		proof.Len(), sent, len(body))

	if sent >= proof.Len() {
		t.Errorf("a %d-byte proof crossed as %d bytes\n  it is the most"+
			" regular thing this engine sends and it goes once per worker per"+
			" layer (E340)", proof.Len(), sent)
	}
}

// A proof that expands without limit is refused, not read.
//
// **What a peer sends is a compressed length, and that says nothing about what
// it becomes.** A few kilobytes of zeroes expand to gigabytes, which is a denial
// of service costing the sender nothing - the same argument every other length
// on this wire is bounded by (maxEntries, maxBody), arriving on a new field.
func TestAProofThatExpandsWithoutLimitIsRefused(t *testing.T) {
	t.Parallel()

	// Far more than maxBlob, from very little.
	huge, err := squeeze(make([]byte, 1<<24))
	if err != nil {
		t.Fatalf("%v", err)
	}

	t.Logf("%d bytes of proof expand to %d", len(huge), 1<<24)

	if len(huge) > 1<<16 {
		t.Fatalf("this corpus does not compress enough to test a bomb: %d bytes",
			len(huge))
	}

	// **Exercised at a small limit, on purpose.** The real one is 8 GiB, which
	// no test can reach in reasonable time or memory - so the *limit* is a
	// parameter and the mechanism is checked at a size a test can hold. A bound
	// that could only be tested by allocating what it exists to prevent would be
	// a bound nobody ever ran.
	_, err = unsqueeze(huge, 1<<20)
	if err == nil {
		t.Error("a proof expanding to 16 MiB passed a 1 MiB limit\n  a" +
			" compressed length says nothing about what it becomes (E340)")
	}

	// And an honest one still arrives.
	fine, err := squeeze([]byte("a modest proof"))
	if err != nil {
		t.Fatalf("%v", err)
	}

	got, err := unsqueeze(fine, 1<<20)
	if err != nil || string(got) != "a modest proof" {
		t.Errorf("an honest proof was refused: %v", err)
	}
}
