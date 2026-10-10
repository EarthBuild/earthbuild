package ir_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The field encoding is pinned to bytes, not to itself.
//
// **Every other digest test compares one computed digest with another**, so a
// change to how a field reaches ℋ - a length prefix dropped, a byte written
// twice, a buffer flushed late - shifts every key in the engine together and
// every one of those tests still passes. What the user sees is a cache that
// silently holds nothing, on every machine, with no test red anywhere.
//
// The vectors in TestBlobDigestsMatchTheReference pin ℋ over a plain byte
// stream, which is the Write path. This pins the framing around it: Count, Str,
// Byte, Bool and Fixed, in an order that would notice any of them moving.
//
// A change here is a cache-generation change (ζ, §4.4) and never a fix on its
// own. Update the constant only alongside one.
func TestTheFieldEncodingIsPinnedToBytes(t *testing.T) {
	t.Parallel()

	const pinned = "030d659a39a1c2137bbde189e6d33b558fb70a71b833b878bb7b7cd2702167ef"

	h := ir.NewHasher()
	h.Count(3)
	h.Str("a/path/with/länge")
	h.Byte(0x2f)
	h.Bool(true)
	h.Bool(false)
	h.Fixed([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	h.Str("")
	h.Count(0)
	h.Str("tail")

	if got := h.Sum().String(); got != pinned {
		t.Errorf("the canonical encoding now digests to\n  %s\n  and was pinned at\n  %s"+
			"\n\n  𝒮 changed, so every key in the engine changed with it: every stored"+
			"\n  entry is now unreachable and every build is cold. If that is intended"+
			"\n  it is a cache-generation change (ζ, green paper §4.4) and this constant"+
			"\n  moves with it; if it is not, the encoding has a defect.", got, pinned)
	}
}
