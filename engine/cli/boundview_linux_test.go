//go:build linux && integration

package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// A step reads the build context through a bound view, for real.
//
// Green paper §3.3d, end to end: the interpreter turns `--mount=type=bind` into
// a view of a context node, the executor matches it to the step's source and
// hands the guest a layer, and the guest binds that layer read-only where the
// step asked for it. Each of those has its own test; none of them shows that
// the four agree.
//
// The step *copies the file out*, so a view that arrived empty, or at the wrong
// path, or holding the wrong bytes fails the build rather than passing quietly.
//
// Not parallel: t.Setenv, which every build test here needs.
func TestAStepReadsTheContextThroughABoundView(t *testing.T) {
	// This process is not the CLI, so it does not serve the agent out of
	// itself - which is the whole of `SelfServesAsGuest`. A test binary has to
	// say where one is, as every build test here does.
	t.Setenv("EARTH_GUESTD", buildGuestd(t))

	const body = "read-through-the-view"

	dir := project(t, `VERSION 0.8

build:
    FROM alpine:3.22
    RUN --mount=type=bind,source=data,target=/data cp /data/f /out.txt
    RUN grep -q `+body+` /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL proof.txt
`, map[string]string{"data/f": body + "\n"})

	var out strings.Builder

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "toomanyrequests") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("a build binding its context failed: %v\n%s", err, out.String())
	}

	got, err := os.ReadFile(filepath.Join(dir, "proof.txt"))
	if err != nil {
		t.Fatalf("the build produced no artifact: %v\n%s", err, out.String())
	}

	if strings.TrimSpace(string(got)) != body {
		t.Errorf("the view delivered %q, want %q", strings.TrimSpace(string(got)), body)
	}
}

// And a step cannot write through one.
//
// I20: the layer store is shared by every step standing on it, so a step
// writing through a view would edit another step's input. The guest binds it
// read-only, and this is that promise seen from inside a real step rather than
// from the mount code.
//
// Not parallel, as above.
func TestAStepCannotWriteThroughABoundView(t *testing.T) {
	t.Setenv("EARTH_GUESTD", buildGuestd(t))

	dir := project(t, `VERSION 0.8

build:
    FROM alpine:3.22
    RUN --mount=type=bind,source=data,target=/data \
        sh -c '! touch /data/written' || (echo "wrote through the view" && false)
    SAVE ARTIFACT /etc/hostname AS LOCAL proof.txt
`, map[string]string{"data/f": "x\n"})

	var out strings.Builder

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "toomanyrequests") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("a step wrote through a read-only view, or the build broke:"+
			" %v\n%s", err, out.String())
	}
}
