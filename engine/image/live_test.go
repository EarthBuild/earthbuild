package image_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestPullAlpineFromDockerHub is the only test here that touches the network,
// and the only one that proves the client speaks to a real registry: token
// auth, a manifest index, and gzipped layers as actually served.
//
// The fake registry cannot establish this - it serves what this client expects,
// which is exactly the assumption under test.
func TestPullAlpineFromDockerHub(t *testing.T) {
	t.Parallel()

	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	dir := t.TempDir()

	_, err := image.Pull(ctx, "alpine:3.22", dir, image.Options{Platform: testPlatform})
	if err != nil {
		t.Fatal(err)
	}

	// A real base image has these. If the layers unpacked but the tree is empty,
	// something silently succeeded at nothing.
	for _, p := range []string{"bin/busybox", "etc/alpine-release"} {
		_, err := os.Stat(filepath.Join(dir, p))
		if err != nil {
			t.Errorf("%s missing from the unpacked image: %v", p, err)
		}
	}
}
