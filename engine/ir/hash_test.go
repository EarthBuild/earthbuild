package ir

import (
	"encoding/hex"
	"math"
	"testing"
)

// TestHashIsBlake3 checks ℋ against the published BLAKE3 test vectors rather
// than merely checking that some hash function is wired up.
//
// Green paper §3.1 fixes ℋ ≡ BLAKE3-256 for the life of the specification, and
// §3.1 also states that changing it invalidates σ. A silent substitution -
// during a dependency bump, say - would therefore be a cache-wide corruption
// presenting as a mysterious loss of hit rate. This test is the tripwire.
//
// Vectors from the BLAKE3 reference implementation's test_vectors.json, whose
// inputs are the repeating byte sequence 0, 1, ..., 250, 0, 1, ...
func TestHashIsBlake3(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		inputLen int
		want     string
	}{
		{0, "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262"},
		{1, "2d3adedff11b61f14c886e35afa036736dcd87a74d27b5c1510225d0f592e213"},
		{1024, "42214739f095a406f3fc83deb889744ac00df831c10daa55189b5d121c855af7"},
	} {
		h := NewHasher()
		h.Fixed(blakeInput(tc.inputLen))

		sum := h.Sum()
		if got := hex.EncodeToString(sum[:]); got != tc.want {
			t.Errorf("input len %d:\n got %s\nwant %s\nℋ is not BLAKE3-256 (green paper §3.1)",
				tc.inputLen, got, tc.want)
		}
	}
}

// TestEncodingIsInjective checks green paper §1.4: distinct field sequences must
// produce distinct byte strings.
//
// Without length prefixing, ⟨"ab","c"⟩ and ⟨"a","bc"⟩ concatenate identically,
// two distinct steps derive one key, and the cache returns the wrong result for
// one of them. That is invariant I3 violated, and it is the failure a build
// system must never have - so it is tested directly rather than inferred from
// the code.
func TestEncodingIsInjective(t *testing.T) {
	t.Parallel()

	seqs := [][]string{
		{"ab", "c"},
		{"a", "bc"},
		{testDigestSeed},
		{testDigestSeed, ""},
		{"", testDigestSeed},
		{"", "", testDigestSeed},
	}

	seen := map[string][]string{}

	for _, seq := range seqs {
		h := NewHasher()
		h.Count(len(seq))

		for _, s := range seq {
			h.Str(s)
		}

		id := h.Sum()
		sum := hex.EncodeToString(id[:])

		if prev, clash := seen[sum]; clash {
			t.Errorf("encoding collision: %q and %q hash alike", prev, seq)
		}

		seen[sum] = seq
	}
}

// TestFixedWidthFieldsCarryNoPrefix checks the other half of §1.4: a
// fixed-width field is written raw, so a 32-byte digest costs 32 bytes and not
// 36. The saving is modest per field and material across a step with tens of
// thousands of inputs.
func TestFixedWidthFieldsCarryNoPrefix(t *testing.T) {
	t.Parallel()

	var id NodeID
	for i := range id {
		id[i] = byte(i)
	}

	raw := NewHasher()
	raw.Fixed(id[:])

	direct := blake3Sum(id[:])

	got := raw.Sum()
	if hex.EncodeToString(got[:]) != direct {
		t.Error("fixed() added framing; a schema-fixed field must be written raw")
	}
}

// blakeInput builds the reference vectors' input: bytes 0..250 repeating.
func blakeInput(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}

	return b
}

func blake3Sum(b []byte) string {
	h := NewHasher()
	h.h.Write(b)
	id := h.Sum()

	return hex.EncodeToString(id[:])
}

// A count that cannot be written is refused, because writing it truncated would
// give two different sequences one encoding (§1.4).
func TestACountTooLargeToEncodeIsRefused(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("a count of more than a u32 was encoded rather than refused")
		}
	}()

	NewHasher().Count(math.MaxUint32 + 1)
}
