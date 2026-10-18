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

// What an L2 hit serves is what a rebuild would have produced.
//
// **The check a newly-live cache tier owes.** Κ₂ began serving results on real
// builds at E125: a `COPY` over a bumped base image is reused when its
// destination is unchanged. That is a claim about bytes, and the only way to
// test a claim about bytes is to produce them both ways.
//
// Two builds of the same Earthfile over the same base image:
//
//	warm   base A, then bump to base B - the copy is served from Κ₂
//	cold   a fresh store, base B from the start - the copy is rebuilt
//
// The artifact has to be identical. A difference is I3 - a false cache hit, the
// one failure this whole design exists to prevent - and it would be invisible in
// every unit test, because a unit test's "base" is a fixture that agrees with
// itself by construction.
func TestAnObservedHitServesWhatARebuildWouldProduce(t *testing.T) { //nolint:paralleltest // boots a sandbox
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	guest := buildGuestd(t)

	// The same copy over two different base images. The file copied and where
	// it lands are identical; only the layers underneath differ.
	earthfile := func(tag string) string {
		return `VERSION 0.8

build:
    FROM alpine:` + tag + `
    COPY src.txt /w/
    SAVE ARTIFACT /w/src.txt AS LOCAL out.txt
`
	}

	run := func(t *testing.T, dir, cache, tag string) string {
		t.Helper()

		err := os.WriteFile(filepath.Join(dir, testEarthfile), []byte(earthfile(tag)), 0o600)
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
			t.Fatalf("build over alpine:%s: %v\n%s", tag, err, log.String())
		}

		return log.String()
	}

	// Warm: built over 3.21, then the base is bumped and the copy is served.
	warm := t.TempDir()
	warmStore := storeDir(t)

	err := os.WriteFile(filepath.Join(warm, "src.txt"), []byte("payload\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	run(t, warm, warmStore, "3.21")
	bumped := run(t, warm, warmStore, "3.22")

	// **The test is worthless without this.** Two builds of one Earthfile
	// produce the same bytes whether or not a cache tier exists, so an
	// assertion about the bytes alone would pass with L2 switched off - which
	// is the shape of a green gate over a feature that is not running (E90).
	if !strings.Contains(bumped, "by observed inputs") {
		t.Fatalf("the bumped build got no observed-input hit, so the comparison"+
			" below is between two ordinary builds and proves nothing:\n%s", bumped)
	}

	// Cold: the same final Earthfile, a store that has never seen 3.21.
	cold := t.TempDir()

	err = os.WriteFile(filepath.Join(cold, "src.txt"), []byte("payload\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	run(t, cold, storeDir(t), "3.22")

	a, err := os.ReadFile(filepath.Join(warm, testArtefact))
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(cold, testArtefact))
	if err != nil {
		t.Fatal(err)
	}

	// What the two builds actually did, so a pass can be read rather than
	// trusted: a suspiciously fast green is the same evidence as a slow one
	// only if you know what ran.
	t.Logf("bumped build:\n%s", bumped)

	if len(a) == 0 || len(b) == 0 {
		t.Fatalf("an artifact is empty: served %d bytes, rebuilt %d", len(a), len(b))
	}

	if !bytes.Equal(a, b) {
		t.Errorf("an observed-input hit served bytes a rebuild would not have produced:"+
			"\n  served  %q\n  rebuilt %q", a, b)
	}
}
