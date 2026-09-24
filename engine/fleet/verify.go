package fleet

import (
	"errors"
	"fmt"
	"io"

	"lukechampine.com/blake3/bao"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ErrCorrupt marks bytes that were not what they claimed to be.
var ErrCorrupt = errors.New("a peer served bytes that do not match their digest")

// groupSize is bao's chunk grouping, as a power of two of 1 KiB chunks.
//
// Sixteen kilobytes per verified group. Smaller means detecting corruption
// sooner and carrying more tree; larger means less overhead and more bytes
// written before a liar is caught. The number matters only for *how soon*,
// which is the property C.4 is about - "within one chunk, not at the end of a
// transfer" - and 16 KiB is early enough that a gigabyte layer is refused after
// a rounding error's worth of it.
const groupSize = 4

// EncodeBlob prepares a blob for verified streaming.
//
// Not `Encode`, which is the assignment's canonical serialisation: two things
// called the same in one package is a reader guessing which they are looking at.
//
// Returns the bytes a sender transmits and the digest a receiver checks them
// against - which is the blob's own id and not a second name for it, because
// BLAKE3's tree root *is* the BLAKE3 hash (see TestBaosRootIsTheBlobsOwnDigest).
func EncodeBlob(b []byte) (stream []byte, id ir.NodeID) {
	return bao.EncodeBuf(b, groupSize, false)
}

// VerifiedCopy writes a stream to dst, verifying as it goes.
//
// **This is what C.4 asks for and a whole-blob hash cannot give.** Hashing the
// bytes at the end detects a liar after the whole transfer; this detects one
// within a group, so a peer serving rubbish costs sixteen kilobytes rather than
// a layer.
//
// dst may receive some bytes before a failure. That is inherent to streaming and
// is why the caller must treat a failed copy's output as nothing - the store
// writes to a temporary file and renames only on success, which is the same
// discipline for the same reason.
func VerifiedCopy(dst io.Writer, stream io.Reader, id ir.NodeID) error {
	ok, err := bao.Decode(dst, stream, nil, groupSize, id)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	if !ok {
		return fmt.Errorf("%w: the stream does not hash to %v", ErrCorrupt, id)
	}

	return nil
}
