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

// A destination that genuinely differs is not reused.
//
// E133 made ownership *translate* between the guest's namespace and the store,
// which is what took a base bump from one copy reused to six. It is also
// exactly the machinery that, wrong in the other direction, would make two
// different bases look alike - and that is a false cache hit, the one failure
// this design exists to prevent (I3).
//
// So the negative is asserted against real images and a real overlay, not
// against fakes that agree with themselves by construction. Two builds whose
// only difference is the **mode** of the directory the copy lands in:
//
//	RUN mkdir -m 700 /app   then   COPY f.txt /app/
//	RUN mkdir -m 755 /app   then   COPY f.txt /app/
//
// `COPY x /app/` places inside a directory and renames onto anything else, and
// what a step can do with a directory it cannot enter is different again - so
// the two copies are not interchangeable and the second must run.
//
// **And it must miss for the right reason.** A miss because L2 was never
// consulted proves nothing about the check; the assertion is that the engine
// consulted the prediction and *refused* it, which its own summary says (E127).
func TestACopyIsNotReusedWhenTheDestinationDiffers(t *testing.T) { // not parallel: boots a sandbox
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	dir := t.TempDir()
	store := storeDir(t)

	err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("payload\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, store)

	build := func(mode string) string {
		t.Helper()

		body := `VERSION 0.8

build:
    FROM alpine:3.22
    RUN /bin/busybox sh -c "mkdir -m ` + mode + ` /app"
    COPY f.txt /app/
`

		err := os.WriteFile(filepath.Join(dir, testEarthfile), []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		var log bytes.Buffer

		err = cli.Run(context.Background(), cli.Options{
			Dir: dir, Target: testTarget, Out: &log, Platform: testPlatform(),
		})
		if err != nil {
			t.Fatalf("build with mode %s: %v\n%s", mode, err, log.String())
		}

		return log.String()
	}

	build("700")
	second := build("755")

	if strings.Contains(second, "L2 hit     COPY") {
		t.Errorf("a copy into a directory with a different mode was reused:"+
			"\n  `COPY x /app/` behaves differently depending on what /app is, so"+
			"\n  reusing the layer serves bytes a rebuild would not have produced\n%s", second)
	}

	// The prediction was consulted and refused, rather than never reached. A
	// miss that skipped the tier entirely would pass the assertion above while
	// proving nothing about the check it is meant to exercise.
	if !strings.Contains(second, "predictions stale") {
		t.Errorf("the tier was not consulted, so this asserts nothing about"+
			" whether it would have refused:\n%s", second)
	}

	if !strings.Contains(second, "/app") {
		t.Errorf("the refusal does not name the path that differed:\n%s", second)
	}
}
