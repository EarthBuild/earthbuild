package guest

import (
	"encoding/json"
	"strings"
	"testing"
)

// A step can ask for a daemon of its own, and what it asks for survives the wire.
//
// The root and the socket are both said, rather than one derived from the other
// by both ends: a host that computes the socket path and a guest that computes
// it again are two implementations of one rule, and the day they disagree the
// daemon listens where nothing looks (the failure E354's `--cache-id` handling
// avoided by deriving the directory in exactly one place, E360).
func TestAStepCanAskForADaemonOfItsOwn(t *testing.T) {
	t.Parallel()

	sent := Request{
		ID: 7, Kind: KindExec, Argv: []string{"docker", "ps"},
		Daemon: &Daemon{Root: "/var/lib/earthbuild-docker", Socket: "/var/run/docker.sock"},
	}

	b, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("a request asking for a daemon will not marshal: %v", err)
	}

	var got Request
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("it will not come back: %v", err)
	}

	if got.Daemon == nil {
		t.Fatal("the request arrived without the daemon it asked for")
	}

	if got.Daemon.Root != sent.Daemon.Root || got.Daemon.Socket != sent.Daemon.Socket {
		t.Errorf("the daemon arrived as %+v, not %+v", *got.Daemon, *sent.Daemon)
	}
}

// A step that wants no daemon says nothing about one.
//
// Every step pays for this field, and all but the few inside a WITH DOCKER want
// nothing to do with it. A pointer rather than a struct so that "no daemon" is
// absent from the wire rather than present and empty - and so that a guest can
// tell "not asked for" from "asked for, with nothing filled in", which is a
// caller bug and should be refused rather than defaulted.
func TestAStepWantingNoDaemonSaysNothing(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(Request{ID: 1, Kind: KindExec, Argv: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(b), "daemon") {
		t.Errorf("an ordinary step carries a daemon field: %s", b)
	}
}

// Asking for a daemon is a version bump, not an added field.
//
// An older guest ignores what it does not know: it would run the body with no
// daemon behind the socket, and the step's first `docker` command would fail
// saying it cannot reach one - a confusing message about a request the guest
// silently declined. That is the silent-disagreement failure the version check
// exists to turn into a refusal, and it is the same argument mounts got at
// version 3 and cancel at version 8.
func TestAskingForADaemonIsAVersionBump(t *testing.T) {
	t.Parallel()

	// The bound rises with each addition, which is the point: this test is a
	// ratchet on the version, not a statement about a particular number.
	if Version <= 14 {
		t.Errorf("a wire change arrived without a version bump: Version is %d, and"+
			" a guest that did not know the newest field would ignore it -"+
			" running a body with no daemon, or binding its whole layer store"+
			" into the step (E366, E398)", Version)
	}
}
