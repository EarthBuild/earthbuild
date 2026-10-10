package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A build deeper than the mount allows still builds, and keeps its oldest work.
//
// Φ (green paper 4.8) collapses the oldest layers of a stack into one so the
// rest can be mounted, and until this test nothing had ever made it happen: the
// threshold was 480 layers, the mount gives out at about 90 (E49), and every
// build in the corpus is shallower than either. So the flattening path had been
// carried, recorded and keyed for months without once running.
//
// It did not work. The scheduler replaced a range of the stack with a single
// identity, wrote that decision into the build record, and handed the executor
// the name of a layer that nothing had built - which the mount would have
// answered by creating an empty directory and mounting it, losing the base of
// the build in silence.
//
// The assertion is deliberately about the *oldest* step's file. The newest
// layers survive any flattening bug at all, because they are the ones Φ keeps;
// what is at risk is everything below the cut, and a test that looked at the
// last file written would pass against an engine that had thrown away the first
// sixty.
//
// Not parallel: boots a VM, see e2e_sandbox_test.go.
func TestABuildDeeperThanTheMountStillKeepsItsBase(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	sh := testShell

	// Deeper than store.MountableStackDepth, so the flattening is real rather
	// than arranged: this is the number of layers at which a mount of full
	// paths stops working, reached by an Earthfile that does nothing unusual.
	const steps = store.MountableStackDepth + 8

	var b strings.Builder

	b.WriteString("VERSION 0.8\n\ndeep:\n    FROM alpine:3.22\n")

	// The first step writes the file everything else is checked against.
	fmt.Fprintf(&b, "    RUN %s -c \"echo oldest > /first.txt\"\n", sh)

	for i := range steps {
		fmt.Fprintf(&b, "    RUN %s -c \"echo step-%d >> /log.txt\"\n", sh, i)
	}

	fmt.Fprintf(&b,
		"    RUN %s -c \"cat /first.txt > /out.txt && wc -l < /log.txt >> /out.txt\"\n", sh)
	b.WriteString("    SAVE ARTIFACT /out.txt AS LOCAL out.txt\n")

	dir := project(t, b.String(), nil)

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "deep", Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("%v\n%s", err, out.String())
	}

	got, err := os.ReadFile(filepath.Join(dir, testArtefact))
	if err != nil {
		t.Fatal(err)
	}

	want := fmt.Sprintf("oldest\n%d\n", steps)
	if strings.Join(strings.Fields(string(got)), " ") != strings.Join(strings.Fields(want), " ") {
		t.Errorf("the deep build lost work below the flattening cut:\n got %q\nwant %q",
			string(got), want)
	}
}
