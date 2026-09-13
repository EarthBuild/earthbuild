package ir

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
	"math"

	"lukechampine.com/blake3"
)

// HashSize is the width of every digest in the engine, in bytes.
//
// Fixed by the specification, which is what lets a digest appear in an encoding
// with neither a length prefix nor an algorithm tag (green paper §1.4, §3.1).
const HashSize = 32

// NewHasher returns ℋ: BLAKE3-256, fixed for the life of the specification and
// not negotiable at runtime (green paper §3.1).
//
// lukechampine.com/blake3 was chosen over github.com/zeebo/blake3 on two
// grounds: it adds one transitive dependency rather than three, and it is a v1
// module. The second matters more than it looks - ℋ determines every cache key
// in the engine, so an API that may change under a v0 compatibility promise is
// a poor place to stand.
func NewHasher() *Hasher {
	h := blake3.New(HashSize, nil)
	bw := bufio.NewWriterSize(h, hashBuffer)

	return &Hasher{h: h, bw: bw, Encoder: Encoder{w: bw}}
}

// hashBuffer is how much encoding is staged before it reaches ℋ.
//
// **The encoding is many small fields and ℋ is fastest given many bytes.** An
// entry is eight writes - a length, a path, a kind byte, a fixed block, a
// digest, two strings and an xattr count - so a tree of 20k entries made 160k
// calls into blake3, most of them a handful of bytes. Staging them changes no
// byte of the stream and so no key; it changes only how often the hash is
// entered. A write larger than this goes straight through (bufio does not
// buffer what will not fit), so streaming a blob's contents still costs one
// copy of nothing.
const hashBuffer = 64 << 10

// DigestOf is ℋ over a byte string, with no framing of any kind (§3.1).
//
// **The primitive a content-addressed store needs**, and the one a Hasher is
// too heavy for: a Hasher carries blake3 state and a staging buffer, so naming
// a thousand small blobs through one allocates a thousand of each. A receiver
// verifying a blob against the name it asked for calls this, and so does
// anything naming an encoding it has already assembled.
//
// Equal to NewHasher().Fixed(b).Sum() by construction - Fixed writes raw - and
// TestDigestOfAgreesWithAHasher holds the two together.
func DigestOf(b []byte) NodeID { return NodeID(blake3.Sum256(b)) }

// Hasher builds the injective encoding required by green paper §1.4.
//
// Fixed-width fields are written raw. Variable-width fields carry a u32 length.
// Sequences carry a u32 count once, not a prefix per element. Getting this
// wrong is not a formatting matter: a non-injective encoding maps two distinct
// steps to one key, which is a false cache hit (I3).
type Hasher struct {
	// Encoder is the encoding itself, which is the same one the wire uses.
	//
	// Separated because §1.4's injective encoding and Appendix B.1's canonical
	// serialisation are **one function**, 𝒮, and a rule implemented twice
	// drifts. A hasher is that encoding with a hash on the end of it; an
	// assignment on the wire is the same bytes into a buffer.
	Encoder

	h  hash.Hash
	bw *bufio.Writer
}

// Encoder writes the canonical encoding of green paper B.1.
//
// Deterministic: maps in ascending key order, no floating point, integers
// fixed-width big-endian, strings length-prefixed UTF-8. Two implementations
// serialising equal values produce equal bytes - without which keys differ
// across implementations and the entire cache is per-implementation.
type Encoder struct {
	w io.Writer

	// one stages a single-byte field, so writing one does not allocate. Byte
	// and Bool are called once per entry each, and `[]byte{b}` escaped.
	one [1]byte
}

// NewEncoder writes the canonical encoding to w.
func NewEncoder(w io.Writer) *Encoder { return &Encoder{w: w} }

// Fixed writes a field whose width the schema fixes, so no prefix is needed.
func (w *Encoder) Fixed(b []byte) { _, _ = w.w.Write(b) }

// Write implements io.Writer, for hashing a single unframed byte stream - a
// blob's contents, where the bytes are the entire message and framing would be
// meaningless.
//
// It is not a general escape hatch. Hashing *fields* through Write instead of
// Str, Count and Fixed loses the length prefixes and with them injectivity,
// which is a false cache hit rather than a style violation (§1.4).
func (w *Encoder) Write(p []byte) (int, error) { return w.w.Write(p) } //nolint:wrapcheck // the writer's own error

// Byte writes a single fixed-width byte.
func (w *Encoder) Byte(b byte) {
	w.one[0] = b
	_, _ = w.w.Write(w.one[:])
}

// Count writes a sequence length, once, ahead of its elements.
//
// **A count that does not fit is refused rather than truncated.** The previous
// `uint32(n)` carried a comment saying it was bounded by the graph's size, which
// is a claim and not a check: a length that wrapped would write a prefix
// belonging to a different sequence, and two distinct sequences sharing an
// encoding is precisely the non-injectivity green paper §1.4 forbids - a false
// cache hit rather than a formatting error (E594).
//
// A panic, because there is no caller that can do anything with an error here
// and every caller is passing `len(...)` of something it just built: reaching
// this means the process holds four billion elements, and continuing with a
// silently wrong key is the worse of the two outcomes.
func (w *Encoder) Count(n int) {
	if n < 0 || n > math.MaxUint32 {
		panic(fmt.Sprintf(
			"encoding a sequence of %d elements: the count is a u32 and this does"+
				" not fit, so the encoding would not be injective (§1.4)", n))
	}

	var buf [4]byte

	binary.BigEndian.PutUint32(buf[:], uint32(n))

	_, _ = w.w.Write(buf[:])
}

// Str writes a variable-width field, length-prefixed so that ⟨"ab","c"⟩ and
// ⟨"a","bc"⟩ cannot collide.
func (w *Encoder) Str(s string) {
	w.Count(len(s))

	// The same bytes either way; WriteString avoids copying the string onto
	// the heap to hand it over, which a path per entry made the dominant
	// allocation in folding a tree.
	if sw, ok := w.w.(io.StringWriter); ok {
		_, _ = sw.WriteString(s)

		return
	}

	_, _ = w.w.Write([]byte(s))
}

// Bool writes a flag as one distinguishable byte.
//
// Written even when false, rather than skipped: a field that only appears when
// set makes the field after it shift position, so a false flag followed by "x"
// would hash the same as no flag followed by "x".
func (w *Encoder) Bool(b bool) {
	var v byte
	if b {
		v = 1
	}

	w.Byte(v)
}

// Sum is the identity of everything written so far.
//
// Truncated to a NodeID, which is 32 bytes: the hash is wider and the identity
// is not. Nothing here re-reads the hasher, so a Sum is the end of an encoding
// rather than a checkpoint in one - and two encodings that differ anywhere
// before this differ here (green paper 1.4).
func (w *Hasher) Sum() NodeID {
	// Everything staged reaches ℋ before it is asked for its answer.
	_ = w.bw.Flush()

	var id NodeID

	copy(id[:], w.h.Sum(nil))

	return id
}
