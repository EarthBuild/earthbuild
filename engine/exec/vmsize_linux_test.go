//go:build linux

package exec

import (
	"runtime"
	"testing"
)

// The guest gets this machine's processors, because the guest *is* the build
// machine.
//
// **Four vCPUs is a serverless default and this is not a function.** A microVM
// here unpacks the layers, runs the steps and does the compiling, while the
// process that started it waits; leaving twenty-eight of thirty-two cores idle
// outside it is the whole build paying for the boundary.
func TestTheGuestGetsThisMachinesProcessors(t *testing.T) {
	t.Parallel()

	if got := defaultCPUs(); got != runtime.NumCPU() {
		t.Errorf("a %d-core machine gives its guest %d", runtime.NumCPU(), got)
	}
}

// Half the memory, because the other half is still this machine's.
//
// A VM's memory is committed: the host cannot use what the guest has been
// given, so taking all of it is how a build takes the machine down with it.
// Half is the conventional split and the one Podman and Docker Desktop use.
func TestTheGuestGetsHalfTheMemory(t *testing.T) {
	t.Parallel()

	got := memoryFrom("MemTotal:       16384000 kB\n")

	if want := 16384000 / 1024 / 2; got != want {
		t.Errorf("a 16GB machine gives its guest %d MiB, wanted %d", got, want)
	}
}

// A machine too small to halve still gets a guest that can build.
func TestASmallMachineStillGetsAWorkableGuest(t *testing.T) {
	t.Parallel()

	if got := memoryFrom("MemTotal:        1048576 kB\n"); got < minMemoryMiB {
		t.Errorf("a 1GB machine gives its guest %d MiB, below the floor of %d",
			got, minMemoryMiB)
	}
}

// Unreadable is the floor rather than zero: a guest given no memory does not
// boot, and this is a default rather than an answer anybody asked for.
func TestAnUnreadableMeminfoFallsBackToTheFloor(t *testing.T) {
	t.Parallel()

	if got := memoryFrom("nothing useful here"); got != minMemoryMiB {
		t.Errorf("an unreadable meminfo gave %d", got)
	}
}
