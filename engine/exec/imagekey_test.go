package exec_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

const (
	someDigest  = "sha256:787328cefd7937073af18fc4b3a725f47e011ffdde9c2908239a25cae6b2f02b"
	otherDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	arm         = "linux/arm64"
)

// Every spelling of one image shares one entry.
//
// A digest *is* the content, so `golang:1.26@sha256:x`, `golang@sha256:x` and a
// mirror serving the same manifest are the same bytes under different names.
// Keyed on the text, `--pin` rewrote a reference this machine had already pulled
// and the next build fetched all of it again - 33s, for bytes that were on the
// disk. A user who takes the advice to pin should not pay for it (E536).
func TestOneDigestIsOneEntry(t *testing.T) {
	t.Parallel()

	tagged := exec.ImageCacheKey("golang:1.26.5-alpine3.24@"+someDigest, arm)
	bare := exec.ImageCacheKey("golang@"+someDigest, arm)
	mirrored := exec.ImageCacheKey("ghcr.io/somebody/golang@"+someDigest, arm)

	if tagged != bare {
		t.Errorf("a tag beside the digest made a second entry:\n  %s\n  %s", tagged, bare)
	}

	if tagged != mirrored {
		t.Errorf("a mirror of the same manifest made a second entry:\n  %s\n  %s", tagged, mirrored)
	}
}

// Different content is a different entry, digest or no digest.
func TestDifferentContentIsADifferentEntry(t *testing.T) {
	t.Parallel()

	if exec.ImageCacheKey("golang@"+someDigest, arm) == exec.ImageCacheKey("golang@"+otherDigest, arm) {
		t.Error("two digests share an entry")
	}

	if exec.ImageCacheKey("golang:1.26", arm) == exec.ImageCacheKey("golang:1.27", arm) {
		t.Error("two tags share an entry")
	}
}

// Platform stays in the key even when a digest is present.
//
// A digest usually names one platform's manifest, and this engine resolves to
// one - but a reference an author wrote by hand may name a manifest *list*, and
// serving one architecture's bytes for another is a container that will not
// start. The cost of keeping it is nothing.
func TestPlatformSurvivesADigest(t *testing.T) {
	t.Parallel()

	if exec.ImageCacheKey("golang@"+someDigest, arm) == exec.ImageCacheKey("golang@"+someDigest, "linux/amd64") {
		t.Error("two platforms share an entry")
	}
}

// A reference with no digest is still keyed by what it says.
//
// Nothing else is known about it: the point of a tag is that it moves, and until
// something resolves it the text is the only identity there is.
func TestAnUnresolvedReferenceIsKeyedByItsText(t *testing.T) {
	t.Parallel()

	if exec.ImageCacheKey("golang:1.26", arm) == exec.ImageCacheKey("golang", arm) {
		t.Error("two unresolved references share an entry")
	}
}
