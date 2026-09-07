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

// The repository's own targets resolve to a plan.
//
// This was a loop somebody ran by hand. It found three engine defects the first
// time (E48) and three more when the plans were actually built (E49), because
// this Earthfile uses constructs no tutorial does: fourteen directories copied
// in three steps and saved as one artifact, `ARG GOOS=$TARGETOS`, a stack deep
// enough to need flattening. A sweep that only happens when somebody remembers
// to sweep is not a ratchet, which is what the test plan said the next piece of
// work was.
//
// Planning rather than building, deliberately. Resolving a plan runs the whole
// front end - parser, interpreter, argument expansion, conditions, loops,
// artifact resolution - and runs only the steps a `$(...)` or an `IF` genuinely
// needs, so it costs a few minutes rather than an hour. What it cannot catch is
// a step that fails when run, which is what `+unit-test` and `+lint` are for and
// why they are run separately.
func TestTheRepositorysOwnTargetsPlan(t *testing.T) { // not parallel: boots a VM, see e2e_sandbox_test.go
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	// The tree under test is not always a checkout - inside the build image
	// `+code` copies source directories and not the Earthfile - and a check
	// that cannot be made is not a check that failed.
	_, err = os.Stat(filepath.Join(root, testEarthfile))
	if err != nil {
		t.Skipf("no Earthfile at the repository root: %v", err)
	}

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	// The targets a developer builds, plus the two that reach furthest into the
	// tree. Not all 32: several want credentials, a registry or a released
	// version, and a ratchet that needs secrets is a ratchet that gets skipped.
	for _, target := range []string{
		"go", "node", "deps", "code",
		"fmt-go", "lint", "unit-test",
		"debugger", "earthly", "all-binaries",
	} {
		var out bytes.Buffer

		err := cli.Run(context.Background(), cli.Options{
			Dir: root, Target: target, Out: &out, DryRun: true, Platform: testPlatform(),
		})
		if err != nil {
			if strings.Contains(err.Error(), "429") {
				t.Skipf("docker hub rate limit: %v", err)
			}

			t.Errorf("+%s does not plan: %v", target, err)

			continue
		}

		// A plan with no steps in it is a target that resolved to nothing, which
		// would pass an error check and mean the sweep measured nothing. Every
		// one of these builds something.
		if !strings.Contains(out.String(), testLocPrefix) {
			t.Errorf("+%s planned no steps:\n%s", target, out.String())
		}
	}
}
