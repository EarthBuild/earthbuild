package ir_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The cheap primitive and the general one name the same bytes the same way.
//
// Two spellings of ℋ is two answers to what a blob is called, and a receiver
// verifying with one against a sender that used the other rejects every blob.
func TestDigestOfAgreesWithAHasher(t *testing.T) {
	t.Parallel()

	for _, b := range [][]byte{nil, {}, {0}, []byte("a byte string"), make([]byte, 1<<17)} {
		h := ir.NewHasher()
		h.Fixed(b)

		if got, want := ir.DigestOf(b), h.Sum(); got != want {
			t.Errorf("DigestOf gave %v and a Hasher gave %v over %d bytes", got, want, len(b))
		}
	}
}
