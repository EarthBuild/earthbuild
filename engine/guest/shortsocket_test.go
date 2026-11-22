package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The daemon listens somewhere that fits in a sockaddr.
//
// `sun_path` is 108 bytes and this is the second limit it has imposed: E375
// moved the exec root off the step, and E396 found the daemon's own listening
// socket still under it. A store path plus a handle plus `merged` plus
// `/var/run/docker.sock` is far past it before anything unusual happens.
//
// So the daemon listens on a short path of the guest's own, and the socket is
// bound into the step afterwards - which is where the step's client looks and
// where the length does not matter, because a bind's target is opened by path
// once and never named in a `sockaddr`.
func TestTheDaemonListensSomewhereThatFits(t *testing.T) {
	t.Parallel()

	at, done, err := shortSocket()
	if err != nil {
		t.Fatalf("%v", err)
	}

	t.Cleanup(done)

	if len(at) > sockaddrLimit {
		t.Errorf("the daemon would listen on a %d-byte path, and the kernel"+
			" refuses over %d: %s", len(at), sockaddrLimit, at)
	}

	if !strings.HasSuffix(at, ".sock") {
		t.Errorf("the path does not name a socket: %s", at)
	}

	// The directory is real, because the daemon binds into it rather than
	// creating it.
	_, err = os.Stat(filepath.Dir(at))
	if err != nil {
		t.Errorf("the directory the daemon will bind in does not exist: %v", err)
	}
}

// Two steps get two sockets.
//
// They run at once - that is the whole point of the scheduler - and a shared
// path would have the second daemon fail to bind or, worse, succeed and be
// reached by the first step's client.
func TestTwoStepsGetTwoSockets(t *testing.T) {
	t.Parallel()

	one, done1, err := shortSocket()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(done1)

	two, done2, err := shortSocket()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(done2)

	if one == two {
		t.Errorf("two concurrent steps would share one socket path: %s", one)
	}
}

// Cleaning up removes it, because a socket left behind is a file in /tmp for
// every WITH DOCKER step a machine has ever run.
func TestTheShortSocketIsCleanedUp(t *testing.T) {
	t.Parallel()

	at, done, err := shortSocket()
	if err != nil {
		t.Fatal(err)
	}

	done()

	_, err = os.Stat(filepath.Dir(at))
	if err == nil {
		t.Errorf("%s survived cleanup", filepath.Dir(at))
	}
}
