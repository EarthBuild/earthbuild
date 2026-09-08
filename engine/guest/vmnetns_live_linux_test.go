//go:build linux

package guest

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// A namespace made without `ip` outlives the call and can be entered again.
//
// **A namespace exists only while something holds it.** Unsharing alone gives
// one that vanishes when the thread returns to its own, so what makes it usable
// by a step started later is the bind mount - and that is the half a test can
// actually check: open the file afterwards, and setns into it.
//
// Run in a user namespace so it needs no root, which is also the position the
// guest agent is in.
func TestANamespaceOutlivesTheCallThatMadeIt(t *testing.T) {
	if os.Getenv("EARTH_NETNS_CHILD") == "" {
		reexecInUserns(t)

		return
	}

	dir := t.TempDir()
	at := filepath.Join(dir, "step-1")

	err := makeNetns(at)
	if err != nil {
		t.Fatalf("make the namespace: %v", err)
	}

	// Still there, and still a namespace: setns is the question a step's
	// launcher will ask, so it is the one worth asking here.
	f, err := os.Open(at)
	if err != nil {
		t.Fatalf("the namespace did not outlive the call: %v", err)
	}

	defer func() { _ = f.Close() }()

	mine, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = mine.Close() }()

	err = unix.Setns(int(f.Fd()), unix.CLONE_NEWNET)
	if err != nil {
		t.Fatalf("the file is not a network namespace: %v", err)
	}

	// Back, so the rest of the test is not run somewhere odd.
	err = unix.Setns(int(mine.Fd()), unix.CLONE_NEWNET)
	if err != nil {
		t.Fatalf("could not return: %v", err)
	}

	err = removeNetns(at)
	if err != nil {
		t.Errorf("release it: %v", err)
	}

	if _, err := os.Stat(at); !os.IsNotExist(err) {
		t.Error("the namespace file survived its removal")
	}
}

// Making one twice is refused rather than silently taking the other's place.
//
// Two builds numbering from zero into one directory is not hypothetical - it is
// what E933 was - and the answer there was to take the next free number, which
// only works if a taken one says so.
func TestAClaimedNamespaceNameIsRefused(t *testing.T) {
	if os.Getenv("EARTH_NETNS_CHILD") == "" {
		reexecInUserns(t)

		return
	}

	dir := t.TempDir()
	at := filepath.Join(dir, "step-1")

	err := makeNetns(at)
	if err != nil {
		t.Fatalf("make the namespace: %v", err)
	}

	defer func() { _ = removeNetns(at) }()

	if err := makeNetns(at); err == nil {
		t.Error("a name already taken was accepted, so one step could take another's network")
	}
}

// reexecInUserns runs the calling test again inside a user namespace, where it
// has the capabilities this needs without being root.
func reexecInUserns(t *testing.T) {
	t.Helper()

	cmd := exec.Command("/proc/self/exe", "-test.run", "^"+t.Name()+"$", "-test.v")
	cmd.Env = append(os.Environ(), "EARTH_NETNS_CHILD=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET,
		UidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: os.Getuid(), Size: 1},
		},
		GidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: os.Getgid(), Size: 1},
		},
		GidMappingsEnableSetgroups: false,
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("in a user namespace: %v\n%s", err, out)
	}
}
