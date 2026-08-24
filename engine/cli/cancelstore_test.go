package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// A cancelled build leaves a store the next build can trust.
//
// The killed case is `TestAKilledBuildLeavesAStoreTheNextBuildCanTrust`, and it
// is the easier one: a process that is gone writes nothing more. A *cancelled*
// build is still running while it unwinds - it releases handles, unmounts, and
// decides what to do with a step whose result it no longer wants - so it has
// the opportunity to leave something behind that a killed one does not.
//
// The property is I9's, and it is about the next build rather than this one: a
// layer is wholly in the store or not in it at all, so a build that follows a
// cancelled one either finds a usable cache entry or misses. What it must never
// find is half of one.
//
// The two builds share a store deliberately. With separate stores this would
// assert nothing at all, which is the way a test like this usually goes wrong.
//
// Not parallel: boots a VM, see e2e_sandbox_test.go.
func TestACancelledBuildLeavesAStoreTheNextBuildCanTrust(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	sh := testShell

	// One step that finishes and is worth caching, then one that does not end.
	// The cancel lands during the second, so the first is a layer being
	// committed while the build around it is being taken apart.
	dir := project(t, `VERSION 0.8

slow:
    FROM alpine:3.22
    RUN `+sh+` -c "echo committed > /first.txt"
    RUN `+sh+` -c "sleep 120"

quick:
    FROM alpine:3.22
    RUN `+sh+` -c "echo committed > /first.txt"
    RUN `+sh+` -c "cat /first.txt > /out.txt"
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`, nil)

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(8 * time.Second)
		cancel()
	}()

	var first bytes.Buffer

	err := cli.Run(ctx, cli.Options{
		Dir: dir, Target: "slow", Out: &first, Platform: testPlatform(),
	})
	if err == nil {
		t.Fatal("the build that was cancelled reported success")
	}

	if strings.Contains(err.Error(), "429") {
		t.Skipf("docker hub rate limit: %v", err)
	}

	// The second build shares the store, and its first RUN is the one the
	// cancelled build had already committed - so it reads whatever that build
	// left behind, which is the whole point.
	var second bytes.Buffer

	err = cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "quick", Out: &second, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("the build after a cancelled one failed: %v\n%s", err, second.String())
	}

	got, err := os.ReadFile(filepath.Join(dir, testArtefact))
	if err != nil {
		t.Fatal(err)
	}

	// Not "it succeeded" but "it read the right bytes": a half-written layer
	// that still mounts is exactly the failure this is about, and a build
	// standing on one succeeds while producing the wrong thing.
	if string(got) != "committed\n" {
		t.Errorf("the build after a cancelled one read %q from a layer the cancelled build wrote",
			string(got))
	}

	// And it must have *reused* that layer, or this test is about a build that
	// redid the work and never touched what the cancelled one left. The shared
	// step is the first RUN, and the outcome column is the engine saying so.
	if !strings.Contains(second.String(), "L1 hit") {
		t.Errorf("the second build reused nothing, so nothing the cancelled build left was"+
			" exercised:\n%s", second.String())
	}
}
