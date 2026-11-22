package ir_test

import (
	"crypto/sha256"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ℋ can be SHA-256, and then every spelling of it is SHA-256.
//
// **A store is built with one function and every digest in it must be that
// one.** REAPI fixes one digest function per conversation, so a store whose
// file contents were hashed one way and whose trees were hashed another names a
// tree that contradicts itself - and no consumer could verify either half.
func TestSelectingSHA256ChangesEverySpellingOfIt(t *testing.T) {
	// Not parallel: this changes a process-wide choice.
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	body := []byte("the quick brown fox")
	want := sha256.Sum256(body)

	if got := ir.DigestOf(body); got != ir.NodeID(want) {
		t.Errorf("DigestOf gave %v, SHA-256 is %x", got, want)
	}

	s := ir.NewStreamHasher()
	if _, err := s.Write(body); err != nil {
		t.Fatal(err)
	}

	if got := s.Sum(); got != ir.NodeID(want) {
		t.Errorf("NewStreamHasher gave %v, SHA-256 is %x", got, want)
	}

	h := ir.NewHasher()
	h.Fixed(body)

	if got := h.Sum(); got != ir.NodeID(want) {
		t.Errorf("NewHasher().Fixed() gave %v, SHA-256 is %x", got, want)
	}
}

// The default is BLAKE3, and nothing has to ask for it.
func TestTheDefaultIsBlake3(t *testing.T) {
	t.Parallel()

	if ir.Hash() != ir.HashBLAKE3 {
		t.Errorf("ℋ defaults to %v; a store built without asking must be BLAKE3", ir.Hash())
	}
}

// The two functions do not agree, which is what makes them different generations.
//
// A store holding both is safe precisely because of this: a key derived under
// one is never a key under the other, so the failure of mixing them is a miss
// rather than a wrong answer (I3).
func TestTheTwoFunctionsDisagree(t *testing.T) {
	body := []byte("a tree")

	blake := ir.DigestOf(body)

	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	if ir.DigestOf(body) == blake {
		t.Error("the two digest functions agree on some input, so a store" +
			" holding both generations could confuse them")
	}
}
