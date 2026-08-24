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

// SOURCE_DATE_EPOCH pins what a build writes; without it, times are true.
//
// Both are right for different builds - byte-reproducible output wants every
// timestamp fixed, an incremental compiler downstream wants them real - so the
// engine takes the instruction instead of choosing, under the name the
// reproducible-builds convention already uses.
//
// End to end because that is the only place it can be checked: the value is
// read on the host for the artifact and forwarded into the guest for the layer,
// and a unit test of either half would pass while the other did nothing.
func TestSourceDateEpochPinsWhatABuildWrites(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	guest := buildGuestd(t)
	sh := testShell

	// The step sets a distinctive mtime, so "preserved" and "clamped" are
	// different from each other *and* from the time the test ran.
	const (
		written = "2001-02-03T04:05:06Z"
		epoch   = "981173106" // 2001-02-03T04:05:06Z, a different instant below
		clamped = int64(981173106)
	)

	build := func(t *testing.T, pin bool) time.Time {
		t.Helper()

		dir := project(t, `VERSION 0.8

build:
    FROM alpine:3.22
    RUN `+sh+` -c "echo x > /out.txt && touch -d '2020-01-02 03:04:05' /out.txt"
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`, nil)

		t.Setenv("EARTH_GUESTD", guest)
		t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))

		if pin {
			t.Setenv("SOURCE_DATE_EPOCH", epoch)
		} else {
			t.Setenv("SOURCE_DATE_EPOCH", "")
		}

		useStore(t, storeDir(t))

		var out bytes.Buffer

		err := cli.Run(context.Background(), cli.Options{
			Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
		})
		if err != nil {
			if strings.Contains(err.Error(), "429") {
				t.Skipf("docker hub rate limit: %v", err)
			}

			t.Fatalf("%v\n%s", err, out.String())
		}

		fi, err := os.Stat(filepath.Join(dir, testArtefact))
		if err != nil {
			t.Fatal(err)
		}

		return fi.ModTime()
	}

	t.Run("pinned", func(t *testing.T) {
		t.Parallel()

		if got := build(t, true).Unix(); got != clamped {
			t.Errorf("the artifact's mtime is %d, want %d - SOURCE_DATE_EPOCH was ignored", got, clamped)
		}
	})

	t.Run("true", func(t *testing.T) {
		t.Parallel()

		got := build(t, false).UTC().Format("2006-01-02")
		if got != "2020-01-02" {
			t.Errorf("the artifact's mtime is %s, want the one the step wrote (2020-01-02)", got)
		}
	})
}
