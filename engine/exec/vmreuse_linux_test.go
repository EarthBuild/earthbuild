//go:build linux

package exec

import (
	"os"
	"testing"
)

// A machine is reused only when it is the machine that was asked for.
//
// **The digest is the safety half and the liveness is the cheap half.** A
// record that names a live process configured differently is the failure the
// Apple backend hit twice - a VM started before a setting existed answers the
// listing, gets reused, and fails much later naming neither (E549, E555). A
// record that names a process which has gone is a build dialling a socket
// nobody holds, which is a boot timeout rather than an answer.
func TestAMachineIsReusedOnlyWhenItIsTheOneAskedFor(t *testing.T) {
	t.Parallel()

	me := os.Getpid()

	for _, c := range []struct {
		name string
		rec  vmRecord
		want string
		ok   bool
	}{
		{"the same machine, running", vmRecord{Digest: "aaa", PID: me, Vsock: "s"}, "aaa", true},
		{"a machine built differently", vmRecord{Digest: "bbb", PID: me, Vsock: "s"}, "aaa", false},
		{"a machine that has gone", vmRecord{Digest: "aaa", PID: 0, Vsock: "s"}, "aaa", false},
		{"a record with no socket", vmRecord{Digest: "aaa", PID: me}, "aaa", false},
	} {
		// This process is not a firecracker, so the ownership check is asked
		// separately; what is under test here is everything else.
		if got := recordMatches(c.rec, c.want); got != c.ok {
			t.Errorf("%s: recordMatches = %v, want %v", c.name, got, c.ok)
		}
	}
}

// The name of a machine changes when what it is changes.
func TestTheMachineNameFollowsItsSettings(t *testing.T) {
	t.Parallel()

	// Built fresh each time rather than copied: a Firecracker carries a mutex,
	// and copying one is the bug vet exists to catch.
	base := func() *Firecracker {
		return &Firecracker{
			Kernel: "/k", Initrd: "/i", StoreImage: "/s", VCPUs: 4, MemoryMiB: 2048,
		}
	}

	was := base().vmDigest()

	for what, change := range map[string]func(*Firecracker){
		"kernel": func(f *Firecracker) { f.Kernel = "/k2" },
		"initrd": func(f *Firecracker) { f.Initrd = "/i2" },
		"store":  func(f *Firecracker) { f.StoreImage = "/s2" },
		"cpus":   func(f *Firecracker) { f.VCPUs = 8 },
		"memory": func(f *Firecracker) { f.MemoryMiB = 4096 },
	} {
		other := base()
		change(other)

		if other.vmDigest() == was {
			t.Errorf("changing the %s leaves the machine with the same name,"+
				" so a build would attach to one built differently", what)
		}
	}
}
