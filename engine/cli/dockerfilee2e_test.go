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

// A Dockerfile another target produces is built, read, and expanded.
//
// The whole of E487 and E488 end to end, and it is here because the unit tests
// could not see what went wrong. Planning was tested with a fake fetcher and the
// wiring with a structural test through `Run`; the first *real* run got past
// both and was refused by the export check - `AS LOCAL "/var/folders/.../
// Dockerfile" would write outside the project` - for a directory the engine had
// chosen itself (E490).
//
// **A seam tested only through its fake is a seam whose other side is untested.**
// What the fakes could not exercise is exactly the layer that failed: a real
// sub-build, producing a real artifact, exported to a real directory.
// Not parallel: boots a sandbox.
func TestADockerfileProducedByATargetBuilds(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	// Before `Available()` is asked anything, for the reason l2run_test.go
	// gives: asking first skips with "cannot find earth-guestd" on every
	// machine that builds this from source.
	guest := buildGuestd(t)
	t.Setenv("EARTH_GUESTD", guest)

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "Earthfile"), []byte(
		"VERSION 0.8\n"+
			"\nmain:\n    FROM DOCKERFILE +gen/\n"+
			"    RUN cat /made-by-the-generated-dockerfile\n"+
			"\ngen:\n    FROM alpine:3.22\n"+
			"    RUN printf 'FROM alpine:3.22\\nRUN echo yes >"+
			" /made-by-the-generated-dockerfile\\n' > Dockerfile\n"+
			"    SAVE ARTIFACT Dockerfile\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	err = cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "+main", Out: &out,
	})
	if err != nil {
		t.Fatalf("building a target whose base comes from a produced"+
			" Dockerfile: %v\n%s", err, out.String())
	}

	// The generated Dockerfile's own step ran - it is what wrote the file - and
	// the consuming step read it. Either alone would pass with the other
	// missing: a base that never ran leaves nothing to read, and a read that
	// never happened says nothing about the base.
	if got := out.String(); !strings.Contains(got, "yes") {
		t.Errorf("the step that reads what the generated Dockerfile wrote"+
			" printed nothing:\n%s", got)
	}
}
