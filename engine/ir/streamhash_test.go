package ir_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The three spellings of ℋ over raw bytes name the same content.
//
// A file hashed by one and verified by another would be a store that rejects
// everything it holds, so the three cannot be allowed to drift.
func TestEveryUnframedHashAgrees(t *testing.T) {
	t.Parallel()

	for _, b := range [][]byte{nil, {}, {7}, []byte("contents"), make([]byte, 1<<18)} {
		framed := ir.NewHasher()
		framed.Fixed(b)

		stream := ir.NewStreamHasher()
		if _, err := stream.Write(b); err != nil {
			t.Fatal(err)
		}

		if got, want := stream.Sum(), framed.Sum(); got != want {
			t.Errorf("stream %v, hasher %v, over %d bytes", got, want, len(b))
		}

		if got, want := ir.DigestOf(b), framed.Sum(); got != want {
			t.Errorf("DigestOf %v, hasher %v, over %d bytes", got, want, len(b))
		}
	}
}
