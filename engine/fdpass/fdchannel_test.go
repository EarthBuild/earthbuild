package fdpass_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fdpass"
)

// The descriptor channel survives being handed to a guest *process*.
//
// E189 passed a descriptor between two ends of a socketpair in one process,
// which is the mechanism and not the arrangement. The guest is a separate
// program: the engine starts `earth-guestd` and talks to it over pipes, so a
// terminal has to reach a different process's address space.
//
// It goes the way the id gate already goes - an extra descriptor on a known
// number, `EARTH_GUEST_ID_GATE=3` being the precedent this repository set. A
// child inherits it, turns it back into a connection, and reads what was sent.
//
// In a child process because that is the claim. Two ends of a socketpair in one
// process would pass while `ExtraFiles`, inheritance and `net.FileConn` on an
// inherited descriptor were all untested - and those are the parts that differ
// between "works here" and "works in the guest".
func TestADescriptorChannelReachesAChildProcess(t *testing.T) {
	if os.Getenv("EARTH_TEST_FD_CHILD") != "" {
		childReadsDescriptor()

		return
	}

	t.Parallel()

	here, there, err := fdpass.SocketPair()
	if err != nil {
		t.Fatalf("no socketpair: %v", err)
	}

	defer here.Close()

	// The child's end, as a file it can inherit.
	theirs, err := there.File()
	if err != nil {
		t.Fatal(err)
	}

	_ = there.Close()

	defer theirs.Close()

	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot find this test binary: %v", err)
	}

	cmd := exec.Command(self, "-test.run", "^TestADescriptorChannelReachesAChildProcess$") //nolint:gosec // this binary
	cmd.Env = append(os.Environ(), "EARTH_TEST_FD_CHILD=1")
	// Inherited as fd 3, the number after the three standard streams - the same
	// place the id gate uses.
	cmd.ExtraFiles = []*os.File{theirs}

	payload := t.TempDir() + "/payload"

	err = os.WriteFile(payload, []byte("through the channel\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(payload) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	var said strings.Builder

	cmd.Stderr = &said

	err = cmd.Start()
	if err != nil {
		t.Fatal(err)
	}

	// After Start, so the child exists to receive it; the socket buffers either
	// way, but sending first would hide an inheritance failure behind a
	// successful write.
	err = fdpass.SendFile(here, f)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	err = cmd.Wait()
	if err != nil {
		t.Fatalf("the descriptor did not reach the child: %v\n%s", err, said.String())
	}

	if !strings.Contains(said.String(), "CHILD-READ-OK") {
		t.Errorf("the child exited cleanly without saying it read anything, so"+
			" this proves only that a process can start:\n%s", said.String())
	}
}

// childReadsDescriptor is the other half, running with fd 3 inherited.
func childReadsDescriptor() {
	c, err := fdpass.ConnFromFD(3)
	if err != nil {
		fmt.Fprintln(os.Stderr, "child: no channel on fd 3:", err)
		os.Exit(3)
	}

	// A deadline, because the interesting failure is *nothing arriving*. Without
	// one the child blocks in RecvFile, the parent blocks in Wait, and the test
	// deadlocks - which in CI is a timeout twenty minutes later rather than a
	// sentence. Found by mutating the send away and watching the suite hang.
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))

	f, err := fdpass.RecvFile(c)
	if err != nil {
		fmt.Fprintln(os.Stderr, "child: no descriptor:", err)
		os.Exit(4)
	}

	b := make([]byte, 64)

	n, _ := f.Read(b)
	if strings.TrimSpace(string(b[:n])) != "through the channel" {
		fmt.Fprintf(os.Stderr, "child: read %q\n", b[:n])
		os.Exit(5)
	}

	// Said out loud, because a child that never ran this function also exits
	// zero - and a test whose only evidence is an exit code cannot tell "it
	// worked" from "it never happened".
	fmt.Fprintln(os.Stderr, "CHILD-READ-OK")
}
