package exec

import (
	"os"
	"strings"
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

	// **This process is not the CLI**, so it has to say it can serve as the
	// agent before the engine may run it as one. That is the whole point of the
	// declaration: the first version assumed it, and this test binary - which
	// has no `guestd` subcommand - was handed back as the agent to three
	// sandbox tests, which then launched it and waited for a protocol that was
	// never going to arrive.
	bin, args, err := guestCommandGiven(true)
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

// A binary that has not said it can be the agent is not made into one.
//
// The lookup runs inside every process that links this package, most of which
// are not the CLI. Answering one of those with itself produces an agent that
// cannot speak the protocol, and the failure lands as a timeout in whatever
// step was unlucky rather than as the missing-file error it actually is.
func TestABinaryThatIsNotTheAgentIsNotOfferedAsOne(t *testing.T) {
	t.Setenv("EARTH_GUESTD", "")

	if selfIsGuest.Load() {
		t.Fatal("this test binary claims to serve the agent, which it does not")
	}

	// A separate agent beside this binary would answer first and legitimately,
	// which is the case the other test covers.
	_, sepErr := findGuestBinary()
	if sepErr == nil {
		t.Skip("an agent is installed beside this binary")
	}

	bin, args, err := guestCommandGiven(false)
	if err == nil {
		t.Fatalf("offered %s %q as the agent; it cannot speak the protocol,"+
			" so the build would hang rather than say what is missing", bin, args)
	}

	// And the refusal still says how to supply one.
	if !strings.Contains(err.Error(), guestBinaryName) {
		t.Errorf("the refusal is %q, which does not name what is missing", err)
	}
}
