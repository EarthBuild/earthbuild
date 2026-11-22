package image

import "lukechampine.com/blake3"

// HashSize is the width of a content digest, in bytes.
//
// **The same width, and the same function, as `engine/ir`.** It cannot be that
// constant: `ir` imports this package for `Healthcheck`, so naming `ir` here
// would be an import cycle. Two declarations of one number is a thing to pin
// rather than to trust, which is what `TestTheUnpackersDigestIsTheEnginesDigest`
// in `engine/exec` does - it can see both packages and asserts they agree over
// the same bytes.
const HashSize = 32

// Digest is a content digest of a file's bytes: blake3-256, no encoding, no
// framing - exactly what `layer.contentDigest` computes by reading the file
// back. It converts to `ir.NodeID` directly, both being [32]byte.
type Digest [HashSize]byte

// NewContentHasher is the one construction of that function in this package.
//
// Exported for the test that pins it against `ir.NewHasher`, which is the only
// thing keeping the two declarations honest - see
// TestTheUnpackersDigestIsTheEnginesDigest in engine/exec.
func NewContentHasher() *blake3.Hasher { return blake3.New(HashSize, nil) }
