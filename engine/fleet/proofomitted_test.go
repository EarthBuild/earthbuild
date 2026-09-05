package fleet

import (
	"bytes"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// fragmentingHeld is a store that can answer with part of a layer and its proof.
type fragmentingHeld struct{ manifest, packed []byte }

func (h fragmentingHeld) Has(ir.NodeID) bool { return true }

func (h fragmentingHeld) Get(ir.NodeID) ([]byte, error) { return h.packed, nil }

func (h fragmentingHeld) Fragment(ir.NodeID, []string) ([]byte, []byte, error) {
	return h.manifest, h.packed, nil
}

// A proof the caller already has is not sent again.
//
// The manifest travels with a fragment because one whose proof arrives
// separately has a state in which it is here and unverifiable, and the only safe
// thing to do in that state is throw it away. But a caller that already holds
// the proof says so, and re-sending it is the dominant cost of a small read set
// (E299): the proof describes the whole layer while the fragment may be a single
// file.
//
// The assertion is on the bytes rather than on a decode, because what is being
// tested is that fewer of them go out. A reader that tolerates both shapes -
// which this one does, since a caller that has the proof does not need the copy
// - cannot tell the two apart, and a test written against the reader would pass
// either way.
func TestAProofTheCallerHasIsNotSentAgain(t *testing.T) {
	t.Parallel()

	held := fragmentingHeld{
		manifest: bytes.Repeat([]byte("M"), 4096),
		packed:   []byte("the fragment itself"),
	}

	id := ir.NodeID{7}
	want := []string{"usr/bin/sh"}

	var withProof, without bytes.Buffer

	err := serveOneBlob(&withProof, held, id, want, true)
	if err != nil {
		t.Fatal(err)
	}

	err = serveOneBlob(&without, held, id, want, false)
	if err != nil {
		t.Fatal(err)
	}

	if without.Len() >= withProof.Len() {
		t.Errorf("asking without the proof sent %d bytes and asking with it"+
			" sent %d: the proof describes the whole layer, and a fragment may"+
			" be one file of it", without.Len(), withProof.Len())
	}

	// And what was omitted is the proof, not the fragment.
	if !bytes.Contains(without.Bytes(), held.packed) {
		t.Error("the fragment itself did not go out")
	}

	if bytes.Contains(without.Bytes(), bytes.Repeat([]byte("M"), 64)) {
		t.Error("the proof went out anyway, to a caller that said it has it")
	}
}
