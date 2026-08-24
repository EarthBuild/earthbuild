package exec

import (
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guestd"
)

// A CLI that arrived somewhere on its own is still able to run a step.
//
// The sandbox agent used to be a second file that had to be beside the CLI, and
// a nested build is precisely the case where it cannot be: a step copies in one
// binary and has nowhere to put a sibling. Every such build failed with
// `cannot find earth-guestd`, which named the missing file and no way to supply
// it (E640).
//
// So the CLI is the agent as well. Nothing is missing, because the thing that
// would have been missing is the thing that is asking.
func TestACliOnItsOwnIsStillItsOwnAgent(t *testing.T) {
	// Not parallel: t.Setenv. Cleared rather than set, because a developer's
	// own $EARTH_GUESTD would otherwise answer before the fallback does.
	t.Setenv("EARTH_GUESTD", "")

	// A directory with no agent in it, which is what a step gets.
	alone := t.TempDir()
	t.Setenv("PATH", alone)

	self, err := os.Executable()
	if err != nil {
		t.Skipf("this platform will not name its own executable: %v", err)
	}

	bin, args, err := findGuestCommand()
	if err != nil {
		t.Fatalf("no agent, with the CLI itself available to be one: %v", err)
	}

	if bin == self {
		if len(args) != 1 || args[0] != guestd.Command {
			t.Errorf("running myself as the agent with args %q,"+
				" which does not select it", args)
		}

		return
	}

	// A separate agent is preferred where one exists - an operator who put it
	// there meant it - so finding one here is correct, not a failure.
	if len(args) != 0 {
		t.Errorf("a separate agent at %s was given the subcommand %q,"+
			" which it does not take", bin, args)
	}
}
