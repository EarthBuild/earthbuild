package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// The engine builds the engine, and what it built runs the next build.
//
// Everything else in this repository's Earthfile builds the BuildKit front end.
// Until now nothing built `earth-native` and `earth-guestd` at all - they were
// `go build` and nothing else - so the engine had never been a consumer of its
// own output.
//
// **That is the difference between self-building and self-hosting.** A build
// that produces a binary proves the steps ran; a build whose binary then runs
// the next build proves the layers were right. Every defect this branch found in
// the last eight iterations - a lost deletion, a flattened hardlink, a dropped
// capability - is the kind that produces a perfectly plausible binary that does
// not work, and none of them would have failed a build.
//
// Two stages, and the second is the point:
//
//  1. build `+native-engine`, which produces both binaries for linux/arm64;
//  2. run an ordinary build using the `earth-guestd` that came out of it, with a
//     deletion in it because that was the last thing to be wrong.
//
// Behind its own switch because stage one is a cold Go build of this whole
// module - minutes, not seconds - and the gate runs on every change.
func TestTheEngineBuildsItselfAndTheResultWorks(t *testing.T) { // not parallel: boots a VM
	if os.Getenv("EARTH_TEST_BOOTSTRAP") == "" {
		t.Skip("set EARTH_TEST_BOOTSTRAP=1 to build the engine with the engine")
	}

	requireSandbox(t)

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer

	err = cli.Run(context.Background(), cli.Options{
		Dir: repo, Target: "native-engine", Out: &log, Platform: testPlatform(),
	})
	if err != nil {
		t.Fatalf("the engine could not build itself: %v\n%s", err, log.String())
	}

	// Where `+native-engine` puts it, which is `build/$GOOS/$GOARCH` - and the
	// build above asked for `testPlatform()`, which is this machine's. It read
	// `arm64`, the **third** assertion on this branch to be about the machine it
	// was written on (E163a found two); this one had never run anywhere, so
	// nothing said so.
	built := filepath.Join(repo, testTarget, "linux", runtime.GOARCH, "earth-guestd")

	fi, err := os.Stat(built)
	if err != nil {
		t.Fatalf("no guest binary came out: %v", err)
	}

	if fi.Size() == 0 {
		t.Fatal("the guest binary is empty")
	}

	// And now the half that matters. A binary that exists proves the steps ran;
	// a binary that *works* proves the layers it was built from were right.
	dir := t.TempDir()

	err = os.WriteFile(filepath.Join(dir, testEarthfile), []byte(`VERSION 0.8

probe:
    FROM alpine:3.22
    RUN echo x > /a.txt && rm /a.txt
    RUN if [ -e /a.txt ]; then echo STILL; else echo GONE; fi > /r.txt
    SAVE ARTIFACT /r.txt AS LOCAL r.txt
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("EARTH_GUESTD", built)

	var second bytes.Buffer

	err = cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testProbe, Out: &second, Platform: testPlatform(),
	})
	if err != nil {
		t.Fatalf("the guest this engine built cannot run a build: %v\n%s", err, second.String())
	}

	body, err := os.ReadFile(filepath.Join(dir, "r.txt"))
	if err != nil {
		t.Fatalf("no artifact: %v\n%s", err, second.String())
	}

	// A deletion, because it was the last thing to be wrong (E88, E94) and
	// because it exercises the whole chain: the marker written at commit, the
	// translation at materialise, the overlay reading it.
	if strings.TrimSpace(string(body)) != "GONE" {
		t.Errorf("a build run by the engine's own guest lost a deletion: %q", body)
	}
}
