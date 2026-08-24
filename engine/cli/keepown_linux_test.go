//go:build linux && integration

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

// Where a file's ownership is lost, in three stages.
//
// `tests/copy-keep-own.earth` asserts `stat -c '%u'` is 1000 after
// `COPY --keep-own +producer/testperms .`, and this engine reports 0. The copy
// implements `--keep-own`, so the ownership was already gone before it - and
// this locates *which* of the three boundaries drops it rather than guessing
// (E446):
//
//	inside one step        chown, then stat, no boundary crossed
//	captured and restored  SAVE ARTIFACT, then COPY it back in the same target
//	across targets         SAVE ARTIFACT in one target, COPY --keep-own in another
//
// A test that names the boundary is worth more than one that asserts the end
// state, because the end state has been failing for a while and says nothing
// about where to look.
func TestWhereOwnershipIsLost(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	guest := buildGuestd(t)

	t.Setenv("EARTH_GUESTD", guest)
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	for _, tc := range []struct {
		name, src string
		// gap marks a stage this engine does not yet carry ownership across.
		// Skipped rather than left red, and skipped *before* it runs so the
		// report cannot be read as a pass: what is written here is the boundary
		// itself, so the day it is fixed this test fails and says so (E446).
		gap string
	}{{
		// One RUN, deliberately. The first version of this case wrote the chown
		// and the stat as two RUNs, which is not one step: each RUN is captured
		// into a layer and materialised again, so both cases were measuring the
		// same boundary and calling it two stages.
		name: "inside one step",
		src: `VERSION 0.8
main:
    FROM alpine:3.22
    RUN adduser -D testuser && touch /f && chown testuser:testuser /f && \
        echo "uid=$(stat -c '%u' /f)" && test "$(stat -c '%u' /f)" = "1000"
`,
	}, {
		// Two RUNs, which is one capture and one materialise between them.
		name: "across one capture, within a target",
		src: `VERSION 0.8
capture:
    FROM alpine:3.22
    RUN adduser -D testuser && touch /f && chown testuser:testuser /f
    RUN test "$(stat -c '%u' /f)" = "1000" || (echo "uid=$(stat -c '%u' /f)"; false)
`,
	}, {
		name: "across targets, through an artifact",
		src: `VERSION 0.8
producer:
    FROM alpine:3.22
    RUN adduser -D testuser && touch /f && chown testuser:testuser /f
    SAVE ARTIFACT /f f

main:
    FROM alpine:3.22
    RUN adduser -D testuser
    COPY --keep-own +producer/f /f
    RUN echo "uid=$(stat -c '%u' /f)"
    RUN test "$(stat -c '%u' /f)" = "1000"
`,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.gap != "" {
				t.Skip(tc.gap)
			}

			dir := t.TempDir()
			err := os.WriteFile(filepath.Join(dir, "Earthfile"), []byte(tc.src), 0o600)
			if err != nil {
				t.Fatal(err)
			}

			ctx, done := context.WithTimeout(context.Background(), 120*time.Second)
			defer done()

			var out bytes.Buffer

			target := "main"
			if strings.Contains(tc.src, "\ncapture:") {
				target = "capture"
			}

			err := cli.Run(ctx, cli.Options{
				Dir: dir, Target: target, Out: &out, Platform: testPlatform(),
			})

			// The uid the build actually saw, whichever way it went.
			said := "(nothing)"
			if i := strings.Index(out.String(), "uid="); i >= 0 {
				said = strings.SplitN(out.String()[i:], "\n", 2)[0]
			}

			if err != nil {
				t.Errorf("%s: %v\n  the build saw %s", tc.name, err, said)
			}
		})
	}
}
