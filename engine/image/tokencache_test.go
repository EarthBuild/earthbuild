package image_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestARegistrysTokenIsFetchedOnce.
//
// **Eleven token exchanges in one build**, 5.5s of a 72s cold `+earthly`:
// `registry:token` six times and `pin:token` five, as the log then read. Each
// was a TLS handshake and a round trip to a token service, and each asked for a
// credential the build was already holding - a bearer token is good for
// minutes, and the whole build takes less than one.
//
// **Eleven lines, but not eleven exchanges.** `pin:token` wrapped the very call
// `registry:token` already timed, so each of the five was a duplicate of one of
// the six: at most six round trips, reported as eleven. The phase has since been
// removed (E733).
//
// The original count was therefore inflated, and the 5.5s with it, since both
// came from reading the log as a list of distinct costs. What the finding rests
// on is unaffected: at six exchanges for one build it was still fetching a
// credential it already held, which is what this test pins down.
//
// The challenge - *where* the token comes from - was already remembered across
// builds. The token itself was not remembered at all, not even for the length
// of one.
//
// Counted rather than timed, because the machine this was found on could not
// be made quiet enough to time anything (E691).
func TestARegistrysTokenIsFetchedOnce(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{gzipTar(t, "f", "one")}, auth: true}
	host := reg.start(t)
	ref := host + "/library/test:1"

	for range 4 {
		_, err := image.Resolve(context.Background(), ref, image.Options{Plain: true})
		if err != nil {
			t.Fatal(err)
		}
	}

	if reg.tokens != 1 {
		t.Errorf("four resolutions of one repository fetched %d tokens, want 1"+
			"\n  a bearer token outlives a build; asking again for one already"+
			"\n  held is a TLS handshake and a round trip for nothing", reg.tokens)
	}
}
