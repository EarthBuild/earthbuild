package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// A build that failed leaves a store the next build can use.
//
// Cancellation has this test - `TestACancelledBuildLeavesAStoreTheNextBuild
// CanTrust` - and **failure does not**, which is the more common event by a
// wide margin: a compile error, a failing test, a typo in a command. Every
// developer's store is mostly the residue of builds that failed.
//
// The two are different. A cancelled build is stopped between steps and its
// cleanup runs; a failed step **ran**, wrote a partial delta into an overlay's
// upper directory, and returned an error from the middle of the capture path.
// What happens to that delta is the question, and nothing had asked it.
//
// The failure is inside the step rather than in the plan, because a plan that
// does not parse never reaches the store and would test nothing.
func TestABuildThatFailedLeavesAUsableStore(t *testing.T) { // not parallel: boots a sandbox
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	dir := t.TempDir()
	store := storeDir(t)

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, store)

	run := func(t *testing.T, body string) (string, error) {
		t.Helper()

		err := os.WriteFile(filepath.Join(dir, testEarthfile), []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer

		err = cli.Run(context.Background(), cli.Options{
			Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
		})

		return out.String(), err
	}

	// A step that writes and *then* fails, so the delta is non-empty when the
	// error happens. A step that fails immediately leaves nothing behind and
	// would pass this test without exercising anything.
	failed, err := run(t, `VERSION 0.8

build:
    FROM alpine:3.22
    RUN /bin/busybox sh -c "echo partial > /half.txt && exit 3"
    SAVE ARTIFACT /half.txt AS LOCAL out.txt
`)
	if err == nil {
		t.Fatal("the failing build reported success")
	}

	// Failed *in the step*, not in the plan. A build that never reached the
	// store leaves nothing behind and would pass everything below while
	// exercising none of it - which is what a plan error, an unavailable image
	// or a missing guest would produce.
	if !strings.Contains(err.Error(), "exit code 3") {
		t.Fatalf("the build failed before the step ran, so nothing was written"+
			" to the store and this test asserts nothing: %v\n%s", err, failed)
	}

	// The same target, now succeeding. It stands on the same base, which the
	// failed build placed in the store.
	log, err := run(t, `VERSION 0.8

build:
    FROM alpine:3.22
    RUN /bin/busybox sh -c "echo whole > /half.txt"
    SAVE ARTIFACT /half.txt AS LOCAL out.txt
`)
	if err != nil {
		t.Fatalf("a build after a failed one could not use the store: %v\n%s", err, log)
	}

	b, err := os.ReadFile(filepath.Join(dir, testArtefact))
	if err != nil {
		t.Fatalf("no artifact: %v\n%s", err, log)
	}

	// And the failed step's partial delta is not what it got. A layer committed
	// from a step that failed would be a cache entry claiming a result that
	// step never produced - the false hit I3 exists to prevent, arriving by way
	// of an error path rather than a key.
	// And nothing was left behind.
	//
	// **A guard here rather than a measurement**: this build's step fails before
	// anything is committed, so no staging directory is created and the check
	// is satisfied by a path that never ran. Mutating the commit's cleanup does
	// not fail it, which is how that was established rather than assumed.
	//
	// It is exercised in `TestTwoBuildsShareAStoreAtOnce`, where two builds
	// stage and commit for real. It stays here because the interesting future
	// leak is exactly this one - a failure part-way through staging - and a
	// guard that costs nothing is worth having where the failure would appear.
	leaks := staging(t, store)
	if len(leaks) != 0 {
		t.Errorf("a failed build left staging directories behind: %v", leaks)
	}

	if got := strings.TrimSpace(string(b)); got != "whole" {
		t.Errorf("the build after a failure produced %q:"+
			"\n  the failed step wrote /half.txt and then exited non-zero, and its"+
			"\n  partial result reached a later build", got)
	}
}

// staging lists half-written directories anywhere under a store.
//
// Named by prefix rather than by path, because the three places that stage -
// image placement, layer commit, whiteout translation - put them in three
// different directories, and a walk that knew where to look would stop knowing
// when a fourth arrives.
func staging(t *testing.T, root string) []string {
	t.Helper()

	var found []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// A directory the guest made unreadable is not a leak this test can
			// see, and refusing to walk it would fail for the wrong reason.
			return nil //nolint:nilerr // see above
		}

		name := d.Name()
		if strings.HasPrefix(name, ".placing-") || strings.Contains(name, ".partial") {
			found = append(found, strings.TrimPrefix(path, root))
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return found
}
