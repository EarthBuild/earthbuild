//go:build linux && integration

package cli_test

import (
	"bytes"
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// A build inside a build: inception, proven by what came out.
//
// The outer build runs `earth-native` inside a `WITH DOCKER --isolate` block,
// and the inner build produces an artefact the outer one carries out. The
// assertion is that artefact's *contents* - a step exiting zero proves the
// command ran, not that a build happened inside it.
//
// Three of this project's findings meet here and all three are load-bearing:
//
//   - the inner build's store is on a **cache mount**, because a step's root is
//     overlayfs and overlayfs cannot stack on itself (E401);
//   - the block says **`--isolate`**, so the inner engine gets a daemon of its
//     own rather than the outer step's - which is the mode a build testing this
//     engine needs (E381);
//   - the image carries a docker **client and no daemon**, which is the design's
//     own claim about where the daemon runs (E368).
func TestABuildInsideABuild(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	_, err := osexec.LookPath("dockerd")
	if err != nil {
		t.Skipf("no dockerd on this machine: %v", err)
	}

	guest := buildGuestd(t)
	cache := storeDir(t)

	// The inner engine, built the way the guest is: static, for this platform.
	native := filepath.Join(t.TempDir(), "earth-native")

	build := osexec.Command("go", testTarget, "-o", native,
		"github.com/EarthBuild/earthbuild/cmd/earth-native")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")

	msg, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build earth-native: %v: %s", err, msg)
	}

	dir := project(t, `VERSION 0.8

build:
    FROM docker:27-cli
    COPY earth-native earth-guestd /usr/local/bin/
    COPY inner.earth /w/Earthfile
    WITH DOCKER --isolate
        RUN --mount=type=cache,target=/ic \
            EARTH_CACHE_DIR=/ic EARTH_GUESTD=/usr/local/bin/earth-guestd \
            earth-native -dir /w +inner && cp /w/inner-out.txt /proof.txt
    END
    SAVE ARTIFACT /proof.txt AS LOCAL proof.txt
`, map[string]string{
		"inner.earth": `VERSION 0.8

inner:
    FROM alpine:3.22
    RUN echo inception > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL inner-out.txt
`,
	})

	for _, from := range [][2]string{{native, "earth-native"}, {guest, "earth-guestd"}} {
		b, err := os.ReadFile(from[0])
		if err != nil {
			t.Fatal(err)
		}

		err := os.WriteFile(filepath.Join(dir, from[1]), b, 0o700)
		if err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("EARTH_GUESTD", guest)
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, cache)

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("the nested build failed: %v\n%s", err, out.String())
	}

	got, err := os.ReadFile(filepath.Join(dir, "proof.txt"))
	if err != nil {
		t.Fatalf("the outer build reported success and carried nothing out: %v\n%s",
			err, out.String())
	}

	if strings.TrimSpace(string(got)) != "inception" {
		t.Errorf("the artefact says %q, and the inner build writes \"inception\";"+
			" an outer step exiting zero is not a build having happened inside it",
			strings.TrimSpace(string(got)))
	}
}
