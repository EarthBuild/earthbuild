package exec

import (
	"context"
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// TestAPrefetchDoesNotRetryAnImageForAnotherPlatform.
//
// **A prediction remembers an image without its platform.** A machine that has
// built for two of them records both, and every later build speculates on both:
// the one for the other platform is fetched - manifest, layer blob and unpack -
// and only then refused at the configuration, because a single-manifest image
// has no arm64 inside it to choose.
//
// Nothing is kept, so it happens again on the next build, and the next. Measured
// on a no-op build it is 0.77s of 1.0s, for ever (E727).
//
// The refusal is permanent in a way a network failure is not, which is why only
// this one is remembered: a timeout should be retried, and an amd64 image will
// not become an arm64 one.
func TestAPrefetchDoesNotRetryAnImageForAnotherPlatform(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	calls := 0

	wrongPlatform := func(context.Context, string, string) (ocispec.ImageConfig, error) {
		calls++

		return ocispec.ImageConfig{}, image.ErrWrongPlatform
	}

	for i := range 3 {
		err := Prefetch(context.Background(), root, "alpine@sha256:abc", "linux/arm64", wrongPlatform)
		if err != nil && i == 0 {
			t.Logf("first attempt reported: %v", err)
		}
	}

	if calls != 1 {
		t.Errorf("the pull was attempted %d times; an image that is for another"+
			" platform will not become this one, so once is all it can be worth", calls)
	}
}

// A failure that might not repeat is not remembered: a registry that timed out
// once is a registry to ask again.
func TestAPrefetchStillRetriesAfterAnOrdinaryFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	calls := 0

	flaky := func(context.Context, string, string) (ocispec.ImageConfig, error) {
		calls++

		return ocispec.ImageConfig{}, errors.New("dial tcp: i/o timeout")
	}

	for range 3 {
		_ = Prefetch(context.Background(), root, "alpine@sha256:abc", "linux/arm64", flaky)
	}

	if calls != 3 {
		t.Errorf("the pull was attempted %d times; a transient failure must not"+
			" be remembered as a permanent one", calls)
	}
}
