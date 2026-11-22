package remote_test

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A result this service did not produce is refused, and said to be in advance.
//
// **The cache is shared with the engine's own steps.** An entry is keyed by Κₜ,
// the Action digest, and that is the same key space a step's result is filed
// under - which is what makes an action's result useful to a later build, and
// what makes accepting somebody else's claim about one unsafe.
//
// The claim cannot be checked. This service can verify that the blobs a result
// names are present and hash to their names; it cannot verify that running the
// action would produce them, because the only way to find out is to run it.
// Storing it anyway is an entry nobody verified, served to every later build
// and every other client.
//
// Bazel uploads results it computed locally unless told not to, so this is a
// thing that happens rather than a thing to worry about.
func TestAResultThisServiceDidNotProduceIsRefused(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	conn := dialService(t, store.DirStore(t.TempDir()))

	in := layer.EncodeGetActionResultForTest(ir.DigestOf([]byte("an action")))
	out := []byte{}

	err := conn.Invoke(context.Background(),
		"/build.bazel.remote.execution.v2.ActionCache/UpdateActionResult", &in, &out)
	if err == nil {
		t.Fatal("a result computed elsewhere was written into this engine's cache")
	}

	// **PERMISSION_DENIED, not UNIMPLEMENTED.** The method is understood and
	// the answer is no; a client told "not implemented" concludes it is talking
	// to an older service and may try another way.
	if got := status.Code(err); got != codes.PermissionDenied {
		t.Errorf("refused with %v", got)
	}

	if !strings.Contains(status.Convert(err).Message(), "did not produce") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// And the capabilities say so before a client asks.
//
// `update_enabled` is absent and so false, which is the answer - but a default
// is a decision nobody made, so it is asserted here where it can be read.
func TestCapabilitiesDoNotOfferActionCacheUpdates(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	caps, err := layer.CapabilitiesIn(layer.EncodeCapabilities(layer.DigestFunctionSHA256, 4<<20))
	if err != nil {
		t.Fatal(err)
	}

	if caps.ActionCacheUpdates {
		t.Error("this service tells a client it accepts uploaded results, and it" +
			" does not - so every upload is a round trip to a refusal")
	}
}
