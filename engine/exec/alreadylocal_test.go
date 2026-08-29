package exec

import (
	"os"
	"path/filepath"
	"testing"
)

// An image already unpacked here is not pulled again, so warming the registry
// for it is a round trip nobody reads.
//
// E907 moved the handshake beside the boot and made cold builds 0.32s faster.
// This check is not about latency: the warm is asynchronous, and three arms at
// five samples each put the warm path's cost below the noise floor. It is about
// the request, which a rate-limited registry counts whether or not anything
// waited for it.
func TestAlreadyLocalReadsTheImageCacheMarker(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const ref, platform = "alpine:3.24.1", "linux/arm64"

	if alreadyLocal(root, ref, platform) {
		t.Error("an empty store reported an image as already local")
	}

	marker := filepath.Join(root, "imagecache", ImageCacheKey(ref, platform)+stackSuffix)
	if err := os.MkdirAll(filepath.Dir(marker), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(marker, []byte("layers"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !alreadyLocal(root, ref, platform) {
		t.Error("an unpacked image was not recognised, so its handshake would be repeated")
	}

	// A different platform is a different image and has its own marker.
	if alreadyLocal(root, ref, "linux/amd64") {
		t.Error("one platform's marker answered for another's")
	}
}

// No store to look in is not an assertion that the image is present: warming
// unnecessarily costs a round trip, skipping wrongly costs the pull's own.
func TestAlreadyLocalSaysNoWithoutAStore(t *testing.T) {
	t.Parallel()

	if alreadyLocal("", "alpine:3.24.1", "linux/arm64") {
		t.Error("an empty image root must not claim the image is local")
	}
}
