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

// A confident wrong prediction does not change what a build does.
//
// **I5, and the safety argument the whole prediction mechanism rests on.** The
// history says which way a condition has been going, so that the work behind
// the likely branch can start before the condition is evaluated. If it could
// also decide the branch, a stale statistic would become a wrong build - and
// the statistic is stale exactly when somebody has just changed something,
// which is when they are looking.
//
// The code says so in three places. `Predictions`' doc: *"the branch a build
// takes is decided by running the condition, never by the prediction"*.
// `recordBranch`'s: *"keeping those two apart is what stops a stale statistic
// from becoming a wrong build"*. `TakeBranch`'s, which exists to make the
// separation structural: *"the predictor is consulted for what to speculate on,
// never for what to do"*.
//
// Three statements of one invariant, and nothing had asserted it end to end.
// The way to assert it is to make the prediction *confidently wrong*: seed a
// history saying this condition goes one way, write an Earthfile where it goes
// the other, and look at which branch the build actually took.
func TestAConfidentlyWrongPredictionDoesNotDecideTheBranch(t *testing.T) { // not parallel: boots a sandbox
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	dir := t.TempDir()
	store := storeDir(t)

	// A condition that is false: the file does not exist.
	body := `VERSION 0.8

build:
    FROM alpine:3.22
    IF [ -f /definitely-not-here ]
        RUN /bin/busybox sh -c "echo TOOK-TRUE > /out.txt"
    ELSE
        RUN /bin/busybox sh -c "echo TOOK-FALSE > /out.txt"
    END
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`

	err := os.WriteFile(filepath.Join(dir, testEarthfile), []byte(body), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, store)

	// Seed a history that is confident and wrong: this site has "always" gone
	// true. Written where the engine keeps it, through the engine's own writer,
	// so the fixture cannot disagree with the format (E122's lesson about
	// hand-written fixtures).
	cli.SeedPrediction(t, store, []string{"[", "-f", "/definitely-not-here", "]"}, "./Earthfile:5", true, 5)

	var log bytes.Buffer

	err = cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testTarget, Out: &log, Platform: testPlatform(),
	})
	if err != nil {
		t.Fatalf("the build failed: %v\n%s", err, log.String())
	}

	out, err := os.ReadFile(filepath.Join(dir, testArtefact)) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("no artifact: %v\n%s", err, log.String())
	}

	if got := strings.TrimSpace(string(out)); got != "TOOK-FALSE" {
		t.Errorf("a confident prediction changed which branch ran: the condition is"+
			"\n  false and the build took %q"+
			"\n  a prediction says what is worth speculating on, never what is true (I5)", got)
	}
}
