package image_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// Warming the token must move the exchange, never add one.
//
// A cold build pays `registry:token` 0.457s *inside* `image:fetch`, after the
// sandbox has booted, even when the reference is pinned and no resolution is
// needed - the pull still authenticates. That exchange is host-side HTTP and
// has no reason to wait behind a 1.48s VM boot (E907).
//
// The risk of doing it early is doing it twice, which would make a cold build
// slower rather than faster. This counts.
func TestWarmingDoesNotAddATokenExchange(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{gzipTar(t, "f", "one")}, auth: true}
	host := reg.start(t)
	ref := host + "/library/test:1"

	image.Warm(context.Background(), ref, image.Options{Plain: true})

	_, err := image.Resolve(context.Background(), ref, image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	if reg.tokens != 1 {
		t.Errorf("warm followed by resolve fetched %d tokens, want 1"+
			"\n  warming is meant to move the exchange earlier, not to add a second one", reg.tokens)
	}
}

// A warm that cannot work must leave the build alone, exactly as Prewarm does:
// it is an optimisation, so its failure is a build that is slower rather than
// one that stops. Nothing is returned, so the only thing to assert is that a
// reference nothing can parse, and a registry that is not there, are survivable.
func TestWarmingIsSilentAboutFailure(t *testing.T) {
	t.Parallel()

	image.Warm(context.Background(), "not a reference at all", image.Options{Plain: true})
	image.Warm(context.Background(), "127.0.0.1:1/library/test:1", image.Options{Plain: true})
}
