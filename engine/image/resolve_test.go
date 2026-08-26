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
// A multi-platform tag names an index, and a *pull* wants the manifest for the
// machine doing it.
//
// This once said pinning the index "would leave the choice of image open,
// which is the thing being pinned down", and that reasoning is wrong for a
// digest written into an Earthfile - see
// TestPinningKeepsTheIndexSoEveryPlatformStillBuilds, and the CI failure that
// belief cost.
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

// TestPinningKeepsTheIndexSoEveryPlatformStillBuilds.
//
// **A digest written into an Earthfile is not the same as a digest used to
// pull.** Pulling wants the platform's own manifest; a *file committed to a
// repository* is built on whatever the reader has, and a platform manifest
// pinned there is an image that exists for one architecture and no other.
//
// This repository proved it: `--pin` was run on arm64 and wrote arm64 manifest
// digests for all 27 base images, after which CI - x86 - failed on the first
// `RUN` with `exec /bin/sh: exec format error`.
//
// Nothing is left open by pinning the index. An index names one exact manifest
// per platform, so the image each architecture builds on is as fixed as it
// would be either way; what changes is only that the other architectures still
// have one.
func TestPinningKeepsTheIndexSoEveryPlatformStillBuilds(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{gzipTar(t, "f", "hello")}, multi: true}
	host := reg.start(t)

	amd, err := image.Resolve(context.Background(), host+"/library/alpine:3.22",
		image.Options{Plain: true, Platform: "linux/amd64", Index: true})
	if err != nil {
		t.Fatalf("amd64: %v", err)
	}

	arm, err := image.Resolve(context.Background(), host+"/library/alpine:3.22",
		image.Options{Plain: true, Platform: "linux/arm64", Index: true})
	if err != nil {
		t.Fatalf("arm64: %v", err)
	}

	if amd != arm {
		t.Errorf("the platforms pinned to %s and %s; a pinned Earthfile has one"+
			" digest and is read on both", amd, arm)
	}

	// And it is genuinely the index, not one platform's manifest that both
	// happened to agree on.
	perPlatform, err := image.Resolve(context.Background(), host+"/library/alpine:3.22",
		image.Options{Plain: true, Platform: "linux/amd64"})
	if err != nil {
		t.Fatal(err)
	}

	if amd == perPlatform {
		t.Error("pinning returned the platform's manifest, which is the digest" +
			" that only builds on the machine that wrote it")
	}
}

// TestPinningASinglePlatformImageStillPinsIt.
//
// "Pin the index if there is one" - and where there is not, the manifest's own
// digest is the pin, because it is the only thing to name. A single-platform
// image has no choice left open for an index to fix.
//
// Worth its own case rather than assumed: `Index` reads as "descend no
// further", and an implementation that took it as "find an index or fail"
// would refuse every image that has only one.
func TestPinningASinglePlatformImageStillPinsIt(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{gzipTar(t, "f", "hello")}}
	host := reg.start(t)

	got, err := image.Resolve(context.Background(), host+"/library/alpine:3.22",
		image.Options{Plain: true, Index: true})
	if err != nil {
		t.Fatalf("a single-platform image could not be pinned: %v", err)
	}

	if !strings.Contains(got, "@sha256:") {
		t.Errorf("resolved to %q, which pins nothing", got)
	}

	// The same answer either way: with no index there is nothing to descend
	// through, so the flag changes nothing at all.
	plain, err := image.Resolve(context.Background(), host+"/library/alpine:3.22",
		image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	if got != plain {
		t.Errorf("pinning gave %s and pulling gave %s; with no index the two"+
			" have nothing to disagree about", got, plain)
	}
}
