package image_test

import (
	"context"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// A tag resolves to the digest of the manifest it names.
//
// This is Θ (green paper §3.4d): the one observation of the outside world a
// build makes that no key can be closed over. Everything downstream keys on what
// this returns, so a moved tag becomes a different build rather than a stale hit
// on the same one (I3).
func TestATagResolvesToADigest(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{gzipTar(t, "f", "hello")}}
	host := reg.start(t)

	got, err := image.Resolve(context.Background(), host+"/library/alpine:3.22",
		image.Options{Plain: true, Platform: "linux/amd64"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if !strings.Contains(got, "@sha256:") {
		t.Fatalf("resolved to %q, which names no content", got)
	}

	if !strings.HasPrefix(got, host+"/library/alpine@sha256:") {
		t.Errorf("resolved to %q, want the same repository at a digest", got)
	}
}

// A reference that already names a digest is returned unchanged.
//
// It is already pinned, and asking a registry to confirm it would make a build
// that is fully pinned depend on the registry being reachable - which is exactly
// what pinning is for avoiding.
func TestADigestReferenceIsNotResolvedAgain(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{gzipTar(t, "f", "hello")}}
	host := reg.start(t)

	want := host + "/library/alpine@sha256:" +
		"1111111111111111111111111111111111111111111111111111111111111111"

	got, err := image.Resolve(context.Background(), want, image.Options{Plain: true})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if got != want {
		t.Errorf("resolved %q to %q; a digest reference is already an answer", want, got)
	}

	if reg.manifests != 0 {
		t.Errorf("fetched %d manifest(s) for a reference that already names content", reg.manifests)
	}
}

// The same tag on two platforms resolves to two different images.
//
// A multi-platform tag names an index; pinning the index would leave the choice
// of image open, which is the thing being pinned down.
func TestATagResolvesPerPlatform(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{gzipTar(t, "f", "hello")}, multi: true}
	host := reg.start(t)

	amd, err := image.Resolve(context.Background(), host+"/library/alpine:3.22",
		image.Options{Plain: true, Platform: "linux/amd64"})
	if err != nil {
		t.Fatalf("amd64: %v", err)
	}

	arm, err := image.Resolve(context.Background(), host+"/library/alpine:3.22",
		image.Options{Plain: true, Platform: "linux/arm64"})
	if err != nil {
		t.Fatalf("arm64: %v", err)
	}

	if amd == arm {
		t.Errorf("both platforms resolved to %s, so the choice is still open", amd)
	}
}
