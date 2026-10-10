package cli_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
	"github.com/EarthBuild/earthbuild/engine/exec"
)

// An image this machine cannot run is refused the second time too.
//
// The architecture check happens while the image's configuration is fetched,
// and a cached image is not fetched - so the refusal held on the first build and
// evaporated on every one after it. What arrived instead was `fork/exec
// /bin/sh: exec format error` at the first RUN: the rootfs was for another
// architecture and the engine found out by executing it.
//
// A corpus sweep produced seven of those, all single-manifest amd64 images, all
// of them a diagnosis the engine already knew how to make. The tell was in the
// message it printed - "if this image was fetched before, clear the image cache
// and build again" - which is a workaround for this bug written down as though
// it were advice.
//
// The second build is the whole test. A single build passes either way, which is
// how this survived: every test that pulled this image had a clean cache.
//
//nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
func TestAnUnrunnableImageIsRefusedFromCacheToo(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	if exec.DefaultPlatform() == "linux/amd64" {
		t.Skip("this machine can run the image, so there is nothing to refuse")
	}

	// A single-manifest amd64 image: there is no other platform to fetch, so
	// the refusal is the only correct answer rather than a preference.
	dir := project(t, `VERSION 0.8

build:
    FROM hashicorp/terraform:light
    RUN terraform version
`, nil)

	t.Setenv("EARTH_GUESTD", buildGuestd(t))

	// Its own image cache, not the shared one: the check under test happens
	// while the configuration is fetched, and an image already in the cache is
	// not fetched. Sharing here would make the test pass or fail according to
	// what some earlier test had pulled.
	t.Setenv("EARTH_IMAGE_CACHE_DIR", t.TempDir())
	useStore(t, storeDir(t))

	// Twice, against one image cache. The first pull populates it; the second
	// is the one that used to succeed at the pull and fail at the first RUN.
	for _, build := range []string{"cold cache", "warm cache"} {
		t.Run(build, func(t *testing.T) {
			var out bytes.Buffer

			err := cli.Run(context.Background(),
				cli.Options{Dir: dir, Target: testTarget, Out: &out})
			if err == nil {
				t.Fatalf("an amd64 image was built on %s\n%s",
					exec.DefaultPlatform(), out.String())
			}

			if strings.Contains(err.Error(), "exec format error") {
				t.Errorf("the mismatch was found by running it:\n%v", err)
			}

			if !strings.Contains(err.Error(), "single-manifest image") {
				t.Errorf("the refusal does not explain the architecture:\n%v", err)
			}
		})
	}
}
