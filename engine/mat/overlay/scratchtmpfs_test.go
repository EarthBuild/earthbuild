package overlay

import (
	"strings"
	"syscall"
	"testing"
)

// Asking for a scratch tmpfs, and being refused for asking wrongly.
//
// Off unless asked for, because tmpfs is memory and a step's upper directory
// holds everything the step wrote: a build producing gigabytes would produce
// them in RAM (E406). What it buys is a quarter of a build's wall clock, which
// is worth an opt-in and not worth a surprise.
//
// **A typo is refused, not ignored.** `EARTH_SCRATCH_TMPFS=4G8` silently
// disabling the feature is the failure this project keeps finding: a mechanism
// that is not running and one that found nothing produce the same output. The
// author would see the old speed and no reason.
func TestTheScratchTmpfsIsAskedForExplicitly(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		env     string
		want    string
		refused bool
	}{
		{name: "unset, which is the default", env: ""},
		{name: "a size", env: "4g", want: "size=4g"},
		{name: "megabytes", env: "512m", want: "size=512m"},
		{name: "a typo", env: "4G8", refused: true},
		{name: "a bare number", env: "4", refused: true},
		{name: "words", env: "yes", refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts, err := scratchTmpfsOptions(tc.env)

			switch {
			case tc.refused:
				if err == nil {
					t.Fatalf("%q was accepted and would have done nothing", tc.env)
				}

				if !strings.Contains(err.Error(), tc.env) {
					t.Errorf("the refusal does not quote what was written: %v", err)
				}

			case err != nil:
				t.Fatalf("%q was refused: %v", tc.env, err)

			case opts != tc.want:
				t.Errorf("options = %q, want %q", opts, tc.want)
			}
		})
	}
}

// A step that fills the tmpfs is told what filled and why it was small.
//
// ENOSPC on a machine with terabytes free is a bewildering error, and it is the
// one failure mode an opt-in tmpfs introduces. The message names the setting, so
// the person who turned it on can turn it off or raise it.
func TestRunningOutOfScratchNamesTheTmpfs(t *testing.T) {
	t.Parallel()

	got := scratchFullHint(syscall.ENOSPC, "size=512m")

	for _, want := range []string{"512m", "EARTH_SCRATCH_TMPFS", "memory"} {
		if !strings.Contains(got, want) {
			t.Errorf("the hint does not mention %q:\n%s", want, got)
		}
	}

	// Nothing where the scratch is an ordinary directory: there the machine is
	// genuinely out of disk and this engine has nothing to add.
	if scratchFullHint(syscall.ENOSPC, "") != "" {
		t.Error("a full disk was blamed on a tmpfs that is not in use")
	}

	// And nothing for other errors, on the rule startHint already follows.
	if scratchFullHint(syscall.EACCES, "size=512m") != "" {
		t.Error("an unrelated error was explained as a full tmpfs")
	}
}
