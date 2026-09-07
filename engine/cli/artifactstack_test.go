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

// An artifact is the whole directory, not the last step's share of it.
//
// A target builds a directory up over several steps - which is what a build
// *is* - and `SAVE ARTIFACT /bundle` names the directory, not the delta. Taking
// only the final step's contribution produces an artifact that is a plausible
// subset of itself: the consumer's COPY succeeds, the files it happens to look
// at first are there, and what is missing is missing quietly.
//
// Found in the repository's own Earthfile. `+code` copies fourteen source
// directories in three COPY steps and saves /earthly; the image holds all of
// them and the artifact held `inputgraph`, the last one written. Two targets
// downstream that surfaces as `find . -name go.mod` returning nothing, which is
// not a sentence anybody can trace back to a copy.
//
// Not parallel: boots a VM, see e2e_sandbox_test.go.
func TestAnArtifactCarriesEveryStepThatBuiltIt(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	sh := testShell

	dir := project(t, `VERSION 0.8

producer:
    FROM alpine:3.22
    RUN `+sh+` -c "mkdir -p /bundle && echo one > /bundle/first.txt"
    RUN `+sh+` -c "echo two > /bundle/second.txt"
    RUN `+sh+` -c "mkdir -p /bundle/nested && echo three > /bundle/nested/third.txt"
    SAVE ARTIFACT /bundle

taker:
    FROM alpine:3.22
    COPY +producer/bundle /placed
    RUN `+sh+` -c "cat /placed/first.txt /placed/second.txt /placed/nested/third.txt > /out.txt"
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`, nil)

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "taker", Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("%v\n%s", err, out.String())
	}

	got, err := os.ReadFile(filepath.Join(dir, testArtefact))
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != "one\ntwo\nthree\n" {
		t.Errorf("the artifact is missing what earlier steps put in it: %q", string(got))
	}
}
