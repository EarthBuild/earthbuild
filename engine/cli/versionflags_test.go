package cli_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A version-flag override reaches the interpreter.
//
// Through `Run` rather than through the option: an option a caller sets and the
// run never reads is *an option accepted and not provided*, which is what E465
// caught about the project argument files - the test called the helper directly
// and passed while nothing else did (E473).
//
// The observable is a refusal naming the flag: an override this engine does not
// know is refused, so the message proves the value arrived.
func TestAVersionOverrideReachesThePlan(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "Earthfile"),
		[]byte("VERSION 0.8\n\nmain:\n    FROM alpine:3.22\n    RUN echo hi\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	base := cli.Options{Dir: dir, Target: "+main", DryRun: true}

	if err := cli.Run(context.Background(), base); err != nil {
		t.Fatalf("the file plans without an override: %v", err)
	}

	with := base
	with.VersionFlags = []string{"no-such-feature"}

	err = cli.Run(context.Background(), with)
	if err == nil {
		t.Fatal("an override naming nothing was accepted, so the option decides nothing")
	}

	if !strings.Contains(err.Error(), "no-such-feature") {
		t.Errorf("refused with %q, which does not name the flag", err)
	}
}

// A deliberate refusal is still a deliberate refusal after `Run` has wrapped it.
//
// The run gate sorts its outcomes with `errors.Is(err, interp.ErrOnPurpose)`, so
// that a target needing `SAVE ARTIFACT --force` reads as a divergence rather than
// as a gap nobody has closed. That sort works only if every layer between the
// refusal and the caller wraps with `%w`, and a sentinel that does not survive
// the trip gives a bucket that can never fill - *a rule that cannot fire is
// indistinguishable from one that is satisfied* (E473).
func TestADeliberateRefusalSurvivesTheRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "Earthfile"),
		[]byte("VERSION 0.8\n\nmain:\n    FROM alpine:3.22\n"+
			"    RUN touch x\n    SAVE ARTIFACT --force x AS LOCAL /tmp/x\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = cli.Run(context.Background(), cli.Options{Dir: dir, Target: "+main", DryRun: true})
	if err == nil {
		t.Fatal("SAVE ARTIFACT --force writes outside the project, and this engine does not")
	}

	if !errors.Is(err, interp.ErrOnPurpose) {
		t.Errorf("refused with %q, which no caller can tell apart from a gap"+
			"\n  every layer between the refusal and here must wrap with %%w", err)
	}
}
