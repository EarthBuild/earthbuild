//go:build linux

package guest

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// A confined step is chrooted into its own filesystem.
//
// **A3 is this line.** The specification assumes the executor confines a step's
// writes to its own upper layer, and `ErrCannotIsolate`'s own documentation says
// what a failure costs: "a step that escapes invalidates *every* cache claim in
// the specification, because ε no longer bounds what it observed."
//
// It had no test. A sweep that deleted `cmd.SysProcAttr.Chroot = root` and ran
// the guest's suite found it green (E242) - because every test that *runs* a
// step either runs it unconfined, or runs as a user for whom `isolate` refuses
// before it reaches this line. The mechanism was exercised by nothing and
// guarded by nothing.
//
// Inside a user namespace, because `isolate` requires euid 0 and refuses
// otherwise - which is correct and is also what hid this.
func TestAConfinedStepIsChrootedIntoItsOwnFilesystem(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	cmd := exec.Command("/bin/true")

	err := isolate(cmd, "/some/root", false)
	if err != nil {
		t.Fatalf("isolate refused inside a user namespace: %v", err)
	}

	if cmd.SysProcAttr == nil {
		t.Fatal("isolate set no process attributes at all")
	}

	if got := cmd.SysProcAttr.Chroot; got != "/some/root" {
		t.Errorf("the step is chrooted into %q, want %q"+
			"\n  without this a step can name paths outside its own filesystem,"+
			" and ε no longer bounds what it observed (A3)", got, "/some/root")
	}

	// The working directory is the new root, not the path the guest knows it by.
	if cmd.Dir != "/" {
		t.Errorf("the working directory is %q; inside the chroot it is /", cmd.Dir)
	}

	for _, want := range []struct {
		flag uintptr
		name string
		why  string
	}{
		{syscall.CLONE_NEWNS, "CLONE_NEWNS", "mounts the step makes would propagate back"},
		{syscall.CLONE_NEWPID, "CLONE_NEWPID", "the step could see and signal the guest"},
		{syscall.CLONE_NEWUTS, "CLONE_NEWUTS", "two steps could observe each other through the hostname"},
		{syscall.CLONE_NEWIPC, "CLONE_NEWIPC", "two steps could observe each other through IPC"},
	} {
		if cmd.SysProcAttr.Cloneflags&want.flag == 0 {
			t.Errorf("%s is not set: %s", want.name, want.why)
		}
	}

	// Not the network, and deliberately: cutting it would break every build
	// that fetches a dependency, so isolation from it is opt-in rather than a
	// side effect of turning confinement on.
	if cmd.SysProcAttr.Cloneflags&syscall.CLONE_NEWNET != 0 {
		t.Error("CLONE_NEWNET is set by default; network isolation is a policy" +
			" decision with a large blast radius and is opt-in")
	}
}

// Asking for no network gets one.
func TestDroppingTheNetworkUnsharesIt(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	cmd := exec.Command("/bin/true")

	err := isolate(cmd, "/some/root", true)
	if err != nil {
		t.Fatal(err)
	}

	if cmd.SysProcAttr.Cloneflags&syscall.CLONE_NEWNET == 0 {
		t.Error("RUN --network=none did not unshare the network namespace")
	}
}

// What a caller set before isolation survives it.
//
// E193: this read `cmd.SysProcAttr = &syscall.SysProcAttr{…}`, which discards
// whatever a caller had already set - and `AttachTerminal` sets fields before
// this runs, so an interactive step got its terminal on the streams and not as a
// *controlling* terminal. Assignment is the natural way to write it and is wrong
// for any field somebody adds later.
func TestIsolationDoesNotDiscardWhatACallerAlreadySet(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	cmd := exec.Command("/bin/true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	err := isolate(cmd, "/some/root", false)
	if err != nil {
		t.Fatal(err)
	}

	if !cmd.SysProcAttr.Setsid {
		t.Error("isolation replaced the process attributes instead of filling" +
			" them in, discarding what a caller had already set (E193)")
	}

	if cmd.SysProcAttr.Chroot != "/some/root" {
		t.Error("and it did not set its own field either")
	}
}
