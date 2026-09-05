package fleet_test

import (
	"bytes"
	"testing"

	"lukechampine.com/blake3/bao"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// bao's root hash is the digest a blob is already addressed by.
//
// The question the whole of chunk verification rests on. If verified streaming
// needed a *different* digest, a blob would have two names - the one the store
// files it under and the one a peer verifies it with - and something would have
// to map between them. It does not: BLAKE3's tree root is the BLAKE3 hash, and
// the group size changes only how much outboard data is carried.
//
// Asserted rather than assumed, because it is a claim about somebody else's
// library and the whole design leans on it.
func TestBaosRootIsTheBlobsOwnDigest(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 1, 1024, 70000} {
		data := bytes.Repeat([]byte("earth"), size)

		_, root := bao.EncodeBuf(data, 4, false)

		if got := fleet.BlobID(data); got != root {
			t.Errorf("%d bytes: the store calls it %v and bao calls it %v;"+
				" a blob with two names needs a mapping nobody has written",
				len(data), got, root)
		}
	}
}
