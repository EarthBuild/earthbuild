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

// A RUN is reused over a base it did not run on, when it read nothing that
// changed.
//
// **What the whole tracer is for.** A COPY has been reusable across a base bump
// since E125, because the guest performs a copy's reads itself and can say what
// they were. A RUN was opaque: its chain key includes the base, so bumping the
// base rebuilt it whether or not anything it looked at had moved.
//
// The base here is **not** a different alpine tag, and that is the design of the
// experiment rather than a convenience. A `RUN` over alpine:3.21 and the same one
// over 3.22 reads a different shell and a different libc, so it *should* miss -
// the step really did read files that changed, and a hit would be the false hit
// I3 forbids. Testing with two tags would measure the tier failing to do
// something it must not do.
//
// So the two bases share their alpine and differ in a file the step never opens.
// The chain key moves, the step's reads do not, and Κ₂ is exactly the claim that
// the second of those decides.
//
// **The COPY is below the divergence on purpose.** With it above, `build` holds a
// copy and a command, and a copy has been reusable across a moved base since
// E125 - so `1 by observed inputs` would be satisfied by the thing that already
// worked, and the RUN could have rebuilt every time without the test noticing.
// Put underneath, the only step that can earn that line is the command.
func TestARunIsReusedOverABaseItDidNotRunOn(t *testing.T) { //nolint:paralleltest // boots a sandbox
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	// The guest is built *before* the sandbox is checked, and the order is not
	// arbitrary: `NewNative().Available()` looks for `earth-guestd`, so asking it
	// first skips with `cannot find earth-guestd` on any machine that has not
	// installed one - which is every machine that builds this from source.
	//
	// `l2same_test.go` asks in the other order and therefore does not run here
	// either. Filed rather than fixed in passing: it is a different test's
	// arrangement and this one should not quietly change it.
	guest := buildGuestd(t)
	t.Setenv("EARTH_GUESTD", guest)

	// `unread` lands in the base and nothing above it opens it. `RUN` reads
	// src.txt, the shell and its libraries - all identical between the two.
	earthfile := func(unread string) string {
		return `VERSION 0.8

layers:
    FROM alpine:3.22
    COPY src.txt /w/
    RUN echo ` + unread + ` > /never-opened.txt

build:
    FROM +layers
    RUN cat /w/src.txt > /w/out.txt
    SAVE ARTIFACT /w/out.txt AS LOCAL out.txt
`
	}

	run := func(t *testing.T, dir, cache, unread string) string {
		t.Helper()

		err := os.WriteFile(filepath.Join(dir, testEarthfile),
			[]byte(earthfile(unread)), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		t.Setenv("EARTH_GUESTD", guest)
		t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
		t.Setenv(testCacheDirEnv, cache)

		var log bytes.Buffer

		err = cli.Run(context.Background(), cli.Options{
			Dir: dir, Target: testTarget, Out: &log, Platform: testPlatform(),
		})
		if err != nil {
			t.Fatalf("build with %q in the base: %v\n%s", unread, err, log.String())
		}

		return log.String()
	}

	warm := t.TempDir()
	warmStore := storeDir(t)

	err := os.WriteFile(filepath.Join(warm, "src.txt"), []byte("payload\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	run(t, warm, warmStore, "one")
	moved := run(t, warm, warmStore, "two")

	t.Logf("the build over the moved base:\n%s", moved)

	// Without this the comparison below is between two ordinary builds and
	// proves nothing - the shape of a green gate over a feature that is not
	// running (E90).
	if !strings.Contains(moved, "by observed inputs") {
		t.Fatalf("the RUN was not served by observed inputs, so its reads did"+
			" not carry it over the moved base:\n%s", moved)
	}

	cold := t.TempDir()

	err = os.WriteFile(filepath.Join(cold, "src.txt"), []byte("payload\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	run(t, cold, storeDir(t), "two")

	a, err := os.ReadFile(filepath.Join(warm, testArtefact)) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(cold, testArtefact)) //nolint:gosec // as above
	if err != nil {
		t.Fatal(err)
	}

	if len(a) == 0 || len(b) == 0 {
		t.Fatalf("an artifact is empty: served %d bytes, rebuilt %d", len(a), len(b))
	}

	if !bytes.Equal(a, b) {
		t.Errorf("an observed-input hit served bytes a rebuild would not have"+
			" produced:\n  served  %q\n  rebuilt %q", a, b)
	}
}
