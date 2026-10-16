//go:build darwin

package exec

import "testing"

// The sandbox's memory ceiling scales with the machine, as its cores do.
//
// **The same bug as `sandboxCPUs`, one line below its fix.** That one records
// what a flat default cost - "every `RUN` on a sixteen-core machine had a
// quarter of it" - and was changed to ask for the machine's own. The memory
// ceiling kept somebody else's figure, so a 128 GiB machine gave a build 8 GiB
// and a Substrate compile was killed by the kernel twice in one afternoon.
//
// `defaultSandboxMemory`'s own comment supplies the argument: it "is a
// *ceiling*, not a reservation - the VM takes what it uses - so the cost of
// being generous is address space rather than memory". EARTH_VM_MEMORY_MIB, for
// the other backend, already documents half the host's memory for this reason.
func TestTheSandboxMemoryScalesWithTheMachine(t *testing.T) {
	t.Parallel()

	const gib = 1 << 30

	for _, tc := range []struct {
		host uint64
		want string
	}{
		{8, "16G"},  // smaller than the floor: the ceiling stops binding, as before
		{16, "16G"}, // the floor
		{32, "16G"}, // half is exactly the floor
		{64, "32G"}, // half
		{128, "64G"},
	} {
		if got := sandboxMemoryFor(tc.host * gib); got != tc.want {
			t.Errorf("a %d GiB machine gives the sandbox %s, want %s", tc.host, got, tc.want)
		}
	}
}

// And a machine too small to halve keeps a usable ceiling.
//
// Below the floor a step runs and its result cannot be captured: writes over
// virtiofs fill the guest's page cache and a `mkdir` into the layer store fails
// with ENOMEM. Halving a 2 GiB machine would produce exactly that, so the floor
// stands above what the machine has - which makes the ceiling stop binding, as
// a flat 8 GiB already did on any machine smaller than that.
func TestTheSandboxMemoryHasAFloor(t *testing.T) {
	t.Parallel()

	if got := sandboxMemoryFor(2 << 30); got != defaultSandboxMemory {
		t.Errorf("a 2 GiB machine gives the sandbox %s, want the floor %s",
			got, defaultSandboxMemory)
	}
}
