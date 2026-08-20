package exec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// An image pulled once is not pulled again for a different step.
//
// The layer store is keyed by node identity, which is right for a step's output
// and wrong for a base image: two targets that both begin `FROM alpine:3.22`
// have different node identities and were pulling the same bytes twice. Keyed
// by reference and platform, the second one is a local copy.
func TestAnImageIsPulledOncePerReference(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	var pulls int

	pull := func(_ context.Context, _, dir string) (ocispec.ImageConfig, error) {
		pulls++

		return ocispec.ImageConfig{}, os.WriteFile(filepath.Join(dir, "layer-content"), []byte("the image\n"), 0o600)
	}

	for _, node := range []string{"node-a", "node-b"} {
		dest := filepath.Join(root, "layers", node)

		err := fetchImage(context.Background(), root, "alpine:3.22", testPlatform, dest, pull)
		if err != nil {
			t.Fatal(err)
		}

		b, err := os.ReadFile(filepath.Join(dest, "layer-content")) //nolint:gosec // a fixture this test wrote
		if err != nil {
			t.Fatalf("%s did not get the image: %v", node, err)
		}

		if string(b) != "the image\n" {
			t.Errorf("%s holds %q", node, b)
		}
	}

	if pulls != 1 {
		t.Errorf("pulled %d times for one reference", pulls)
	}
}

// A different platform is a different image.
//
// The same name on two architectures is two sets of bytes, and serving one for
// the other is a container that will not start - the failure being avoided here
// is worse than the pull being saved.
func TestPlatformIsPartOfTheKey(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	var pulls int

	pull := func(_ context.Context, _, dir string) (ocispec.ImageConfig, error) {
		pulls++

		return ocispec.ImageConfig{}, os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o600)
	}

	for _, p := range []string{testPlatform, testOtherPlatform} {
		dest := filepath.Join(root, "layers", "node-"+p)

		err := fetchImage(context.Background(), root, "alpine:3.22", p, dest, pull)
		if err != nil {
			t.Fatal(err)
		}
	}

	if pulls != 2 {
		t.Errorf("pulled %d times for two platforms, want one each", pulls)
	}
}

// A pull that fails leaves nothing behind to be mistaken for the image.
//
// A half-written cache entry is worse than none: the next build finds a
// directory, believes the image is there, and builds on a fragment.
func TestAFailedPullLeavesNoCacheEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	fail := func(_ context.Context, _, dir string) (ocispec.ImageConfig, error) {
		err := os.WriteFile(filepath.Join(dir, "partial"), []byte("half"), 0o600)
		if err != nil {
			return ocispec.ImageConfig{}, err
		}

		return ocispec.ImageConfig{}, os.ErrDeadlineExceeded
	}

	dest := filepath.Join(root, "layers", "node-a")

	err := fetchImage(context.Background(), root, "alpine:3.22", testPlatform, dest, fail)
	if err == nil {
		t.Fatal("a failed pull reported success")
	}

	var pulls int

	ok := func(_ context.Context, _, dir string) (ocispec.ImageConfig, error) {
		pulls++

		return ocispec.ImageConfig{}, os.WriteFile(filepath.Join(dir, "layer-content"), []byte("the image\n"), 0o600)
	}

	err = fetchImage(context.Background(), root, "alpine:3.22", testPlatform, dest, ok)
	if err != nil {
		t.Fatal(err)
	}

	if pulls != 1 {
		t.Error("the second attempt was served the wreckage of the first")
	}

	_, err = os.Stat(filepath.Join(dest, "partial"))
	if err == nil {
		t.Error("the half-written pull survived into the layer")
	}
}

// The image cache can live apart from the build cache.
//
// An image is content-addressed by reference and platform and is identical for
// every project on a machine, so isolating it per build cache means pulling
// alpine again for every project - and, in this repository's own test suite,
// for every run. That is how a day of testing earned a rate limit from Docker
// Hub: the suite gave each case a fresh cache directory, which is right for
// layers and wrong for images.
func TestTheImageCacheCanBeSharedAcrossBuildCaches(t *testing.T) {
	t.Parallel()

	shared := t.TempDir()

	var pulls int

	pull := func(_ context.Context, _, dir string) (ocispec.ImageConfig, error) {
		pulls++

		return ocispec.ImageConfig{}, os.WriteFile(filepath.Join(dir, "layer"), []byte("bytes\n"), 0o600)
	}

	// Two builds with entirely separate stores, one shared image cache.
	for _, store := range []string{t.TempDir(), t.TempDir()} {
		dest := filepath.Join(store, "layers", "node")

		err := fetchImageFrom(context.Background(), shared, "alpine:3.22", testPlatform, dest, pull)
		if err != nil {
			t.Fatal(err)
		}

		_, err = os.Stat(filepath.Join(dest, "layer"))
		if err != nil {
			t.Fatalf("the image did not arrive in %s: %v", store, err)
		}
	}

	if pulls != 1 {
		t.Errorf("pulled %d times across two build caches sharing one image cache", pulls)
	}
}
