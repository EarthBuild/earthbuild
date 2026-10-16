package remote

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// BlobName is the digest a bytestream resource name refers to.
//
// **Found by its markers, because its shape is not fixed.** REAPI's templates
// are `{instance}/blobs/{fn/}{hash}/{size}` to read and
// `{instance}/uploads/{uuid}/blobs/{fn/}{hash}/{size}{/metadata}` to write, and
// `instance` may be empty *or* contain slashes - so counting segments from
// either end gets the wrong answer for some conforming client. The literal
// `blobs` segment is the only thing that can be relied on: everything before it
// is a name this service does not use, and anything after the size is metadata
// a client attached for itself.
//
// The digest function segment is optional and omitted for the standard ones, so
// whichever of the two following segments parses as a digest is the digest.
func BlobName(resource string) (ir.NodeID, int64, error) {
	segs := strings.Split(strings.Trim(resource, "/"), "/")

	at := -1

	for i, s := range segs {
		switch s {
		case "blobs":
			at = i
		case "compressed-blobs":
			// Refused rather than decompressed: this service advertises no
			// compressor, so a client asking for one was told by somebody else.
			return ir.NodeID{}, 0, fmt.Errorf(
				"%q asks for a compressed blob, and this service does not compress"+
					"\n  it advertises no compressor in its capabilities, so ask for"+
					" `blobs/` rather than `compressed-blobs/`", resource)
		}
	}

	if at < 0 {
		return ir.NodeID{}, 0, fmt.Errorf(
			"%q names no blob: a resource name has a `blobs` segment, after which"+
				" come the hash and the size", resource)
	}

	// The hash is the next segment, unless that is a digest function's name and
	// the hash is the one after it.
	for _, i := range []int{at + 1, at + 2} {
		if i >= len(segs) {
			continue
		}

		id, err := ir.ParseNodeID(segs[i])
		if err != nil {
			continue
		}

		if i+1 >= len(segs) {
			return ir.NodeID{}, 0, fmt.Errorf(
				"%q names a blob and no size: a digest is a hash and a size", resource)
		}

		size, err := strconv.ParseInt(segs[i+1], 10, 64)
		if err != nil {
			return ir.NodeID{}, 0, fmt.Errorf(
				"%q gives the size as %q, which is not a number", resource, segs[i+1])
		}

		return id, size, nil
	}

	return ir.NodeID{}, 0, fmt.Errorf(
		"%q has a `blobs` segment and no digest after it", resource)
}
