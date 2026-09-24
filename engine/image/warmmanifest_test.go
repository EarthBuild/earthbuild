package image_test

import (
	"context"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// A pinned manifest fetched by the warm is not fetched again by the pull.
//
// `registry:manifest` is 0.135s of a cold build and host-side, so it can run
// beside the boot exactly as the token does (E907). It is safe to reuse only
// because the target is a digest: the bytes behind one cannot change, so a
// cached body is the same answer and not a stale one.
//
// Docker Hub allows an anonymous puller 100 manifest requests an hour, which
// this repository's own fake registry documents - so the request saved matters
// beyond the milliseconds.
func TestWarmingAPinnedManifestRemovesThePullsFetch(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{gzipTar(t, "f", "one")}, auth: true}
	host := reg.start(t)

	pinned, err := image.Resolve(context.Background(), host+"/library/test:1", image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(pinned, "@sha256:") {
		t.Fatalf("expected a digest-pinned reference, got %q", pinned)
	}

	reg.manifests = 0

	image.Warm(context.Background(), pinned, image.Options{Plain: true})

	afterWarm := reg.manifests
	if afterWarm != 1 {
		t.Fatalf("warming a pinned reference made %d manifest requests, want 1", afterWarm)
	}

	_, _, err = image.PullApart(context.Background(), pinned, t.TempDir(), image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	if reg.manifests != 1 {
		t.Errorf("warm then pull made %d manifest requests, want 1"+
			"\n  the pull refetched a manifest the warm had already read, and a"+
			"\n  digest's bytes cannot have changed in between", reg.manifests)
	}
}

// An unpinned reference is not cached: a tag can move, and serving yesterday's
// answer for one is the failure this engine exists to prevent.
func TestWarmingATagDoesNotCacheItsManifest(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{gzipTar(t, "f", "one")}, auth: true}
	host := reg.start(t)
	ref := host + "/library/test:1"

	image.Warm(context.Background(), ref, image.Options{Plain: true})

	reg.manifests = 0

	_, err := image.Resolve(context.Background(), ref, image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	if reg.manifests != 1 {
		t.Errorf("resolving a tag made %d manifest requests, want 1"+
			"\n  a tag must be asked about every time; only a digest may be remembered", reg.manifests)
	}
}
