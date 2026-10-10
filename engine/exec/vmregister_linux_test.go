//go:build linux

package exec

import (
	"os"
	"path/filepath"
	"testing"
)

// A machine is recorded where the next build will look for it.
//
// **Because a microVM has no `container ls`.** The Apple backend finds the VM
// the last build left running by asking its runtime what is up; firecracker has
// no runtime to ask, so the register beside the store is what makes the machine
// findable at all. Everything reuse depends on hangs off this: the digest that
// says whether the machine is the one wanted, the socket to reach it on, and
// the pid that says whether it is still there.
func TestAMachineIsRecordedAndFoundAgain(t *testing.T) {
	t.Parallel()

	store := filepath.Join(t.TempDir(), "store.img")

	want := vmRecord{Digest: "abc123", Vsock: "/tmp/x/guest.vsock", PID: os.Getpid()}

	err := writeVMRecord(store, want)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := readVMRecord(store)
	if !ok {
		t.Fatal("a machine written to the register was not found again")
	}

	if got != want {
		t.Errorf("the register gave back %+v, want %+v", got, want)
	}
}

// No record is not an error: the first build of a machine finds nothing.
func TestAnAbsentRecordIsNotAFailure(t *testing.T) {
	t.Parallel()

	store := filepath.Join(t.TempDir(), "store.img")

	if _, ok := readVMRecord(store); ok {
		t.Error("a register that was never written reported a machine")
	}
}

// A record naming a process that has gone names no machine.
//
// The pid is the cheap half of liveness and the handshake is the honest half:
// this stops a build dialling a socket whose owner died, which is a thirty
// second timeout rather than an answer.
func TestARecordForADeadProcessIsNotLive(t *testing.T) {
	t.Parallel()

	if vmAlive(vmRecord{PID: os.Getpid()}) != true {
		t.Error("this process reads as not running")
	}

	// Pid 0 is never a process; a record that lost its pid must not look live.
	if vmAlive(vmRecord{PID: 0}) {
		t.Error("a record with no pid reads as a running machine")
	}
}

// A corrupt register is an absent one, not a failed build.
//
// It is a cache of where a machine is. The worst a bad one may cost is booting
// a second machine, and the worst it may do is stop the build.
func TestACorruptRegisterReadsAsAbsent(t *testing.T) {
	t.Parallel()

	store := filepath.Join(t.TempDir(), "store.img")

	err := os.WriteFile(vmRecordPath(store), []byte("{not json"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := readVMRecord(store); ok {
		t.Error("a register nobody can parse reported a machine")
	}
}

// The register carries the export device, because a build that attaches never
// makes one.
//
// **The device is made at boot and lives in the booting build's sandbox
// directory.** A build that joins a running machine skips that step entirely,
// so without this it holds an empty path and every `SAVE ARTIFACT AS LOCAL`
// fails on `os.Open("")` - which reads as a broken export and not as a machine
// that was joined.
func TestTheRegisterCarriesTheExportDevice(t *testing.T) {
	t.Parallel()

	store := filepath.Join(t.TempDir(), "store.img")

	want := vmRecord{
		Digest: "d", Vsock: "/tmp/s/guest.vsock", PID: os.Getpid(),
		Exports: "/tmp/s/exports.img",
	}

	err := writeVMRecord(store, want)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := readVMRecord(store)
	if !ok {
		t.Fatal("not found")
	}

	if got.Exports != want.Exports {
		t.Errorf("the register gave back exports %q, want %q"+
			"\n  a build that attaches has no other way to learn it",
			got.Exports, want.Exports)
	}
}
