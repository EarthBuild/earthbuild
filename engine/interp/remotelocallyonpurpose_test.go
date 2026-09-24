package interp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// Refusing a fetched `LOCALLY` is a position, and says so.
//
// **An intentional refusal that does not declare itself is a bug to everyone
// who meets it.** This engine has three words for declining a construct and
// they promise different things: `unsupported` promises later, `notInLanguage`
// promises another construct, and `refusedOnPurpose` promises nothing, because
// the construct works and the engine will not do it.
//
// Refusing `LOCALLY` in an Earthfile fetched from a repository is squarely the
// third - it defends the machine from a command chosen by whoever can push
// there (green paper §5.3, E439) - and it was a bare error saying none of that.
// The corpus read it as a defect for exactly that reason, while three sibling
// targets refusing privileged remotes were read as deliberate, the only
// difference being that those say "on purpose".
//
// `ErrOnPurpose` is the machine-readable half of that sentence, and this asserts
// it rather than the wording, because the wording is for people.
func TestAFetchedLocallyIsRefusedOnPurpose(t *testing.T) {
	t.Parallel()

	f := hostileRemote(t)

	_, err := interp.Build(versioned+"\nmain:\n    FROM github.com/org/repo+dangerous\n",
		testMain, interp.WithRemotes(f.fetch))
	if err == nil {
		t.Fatal("a remote target running LOCALLY was built")
	}

	if !errors.Is(err, interp.ErrOnPurpose) {
		t.Errorf("refused with %q, which is not marked as a decision"+
			"\n  a caller cannot tell this from a gap, and a gap is an"+
			" invitation to close it - here that means running a fetched"+
			" repository's commands on this machine", err)
	}

	// The two facts a reader needs are still there.
	for _, want := range []string{"LOCALLY", "github.com/org/repo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %q", err, want)
		}
	}
}
