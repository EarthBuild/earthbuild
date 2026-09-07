//go:build linux && integration

package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// A build that outlives its context is a build nothing can stop.
//
// The execution gate gives each target 60 seconds and moves on. Raised to 40
// files it stopped at `tests/build-arg.earth` and sat there for thirteen
// minutes, which means the deadline reached nobody: `cli.Run` returned only when
// the work did (E442).
func TestABuildStopsWhenItsContextDoes(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	guest := buildGuestd(t)

	t.Setenv("EARTH_GUESTD", guest)
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	root := os.Getenv("EARTH_CORPUS_DIR")
	src, err := os.ReadFile(filepath.Join(root, "tests", "build-arg.earth"))
	if err != nil {
		t.Skipf("no corpus: %v", err)
	}

	dir := t.TempDir()
	err = os.WriteFile(filepath.Join(dir, "Earthfile"), src, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	ctx, done := context.WithTimeout(context.Background(), 20*time.Second)
	defer done()

	var out bytes.Buffer

	began := time.Now()
	_ = cli.Run(ctx, cli.Options{Dir: dir, Target: "all", Out: &out, Platform: testPlatform()})
	took := time.Since(began)

	if took > 40*time.Second {
		t.Errorf("the build ran for %v under a 20-second deadline"+
			"\n  something in it does not watch the context, so nothing can"+
			" interrupt a build that is stuck", took)
	}
}
