package guest

import (
	"errors"
	"strings"
	"testing"
)

// Where the daemon's binary is, is said rather than derived.
//
// **`Daemon` already says the other two.** Its socket is said because "two
// implementations of one rule disagree eventually", and its root is said
// because where the storage lives is the executor's decision and not the
// guest's (E365). The binary was the one part the guest went and found - and
// `LookPath` means two different things depending on where the guest happens to
// be running. Inside a VM it resolves in the sandbox image, which is pinned by
// digest; under the native backend it resolves on the host, which is whatever
// is installed there. Nobody decided that; it follows from the process's
// location.
//
// Saying it changes nothing today - the host sends nothing and the lookup is
// what it was - and it is the seam the rest plugs into: a host that can name
// the binary can name one it materialised, on either backend, from an image it
// pinned.
func TestTheDaemonsBinaryIsSaidWhenTheHostSaysIt(t *testing.T) {
	t.Parallel()

	var asked []string

	look := func(name string) (string, error) {
		asked = append(asked, name)

		return "/found/on/path/" + name, nil
	}

	// Said: the lookup is not consulted at all.
	_, err := launchWith(t.Context(), look, nil, "/sock", "", "/from/the/host/dockerd")
	if len(asked) != 0 {
		t.Errorf("the host named the binary and the guest went looking anyway: %v", asked)
	}

	// It fails to start, because that path holds nothing - which is the point:
	// the refusal names what the host asked for rather than something found.
	if err == nil || !strings.Contains(err.Error(), "/from/the/host/dockerd") {
		t.Errorf("starting a binary that is not there said: %v", err)
	}

	// Not said: the lookup is what it always was.
	asked = nil

	_, _ = launchWith(t.Context(), look, nil, "/sock", "", "")

	if len(asked) != 1 || asked[0] != "dockerd" {
		t.Errorf("with nothing said the guest looked for %v, and it looks for dockerd", asked)
	}
}

// A binary the host named and that is not there is refused by name.
//
// The message matters more here than usual: every message about an unreachable
// daemon suggests installing Docker in the image, and that is exactly the wrong
// advice - the daemon runs beside the step. One that says which path was asked
// for tells whoever reads it which of the two machines is missing it.
func TestANamedDaemonBinaryThatIsAbsentSaysWhich(t *testing.T) {
	t.Parallel()

	never := func(string) (string, error) { return "", errors.New("should not be consulted") }

	_, err := launchWith(t.Context(), never, nil, "/sock", "", "/no/such/dockerd")
	if err == nil {
		t.Fatal("a daemon binary that is not there started")
	}

	if !strings.Contains(err.Error(), "/no/such/dockerd") {
		t.Errorf("the refusal does not name the path the host asked for: %v", err)
	}
}
