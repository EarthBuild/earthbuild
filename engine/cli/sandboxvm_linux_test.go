//go:build linux

package cli

import (
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// Asked for a microVM, this platform gives one.
//
// The choice is explicit rather than automatic: a machine with `/dev/kvm` is
// most machines, and silently moving every build into a VM changes what a step
// can reach, how long a boot takes and where the layers live. The stronger
// boundary is offered, not imposed (I11).
func TestAMicroVMIsUsedWhenAskedFor(t *testing.T) { // not parallel: sets the environment
	t.Setenv(envVM, "1")
	t.Setenv("EARTH_VM_KERNEL", vmArtefact(t, "vmlinux"))
	t.Setenv("EARTH_VM_INITRD", vmArtefact(t, "initrd.cpio.gz"))

	sb, err := sandbox("")
	if err != nil {
		t.Skip("no microVM on this machine: ", err)
	}

	if _, ok := sb.(*exec.Firecracker); !ok {
		t.Errorf("asked for a microVM and got %T", sb)
	}
}

// Not asked for, it is the namespace backend, which is every build today.
func TestTheNamespaceBackendIsTheDefault(t *testing.T) { // not parallel: sets the environment
	t.Setenv(envVM, "")

	sb, err := sandbox("")
	if err != nil {
		t.Skip("no sandbox on this machine: ", err)
	}

	if _, ok := sb.(*exec.Native); !ok {
		t.Errorf("the default backend is %T, not the namespace one", sb)
	}
}

// Asked for and unavailable, the build says so rather than quietly running
// outside the boundary it was told to use.
//
// **The degrade is at the choice, not after it.** `Available` reports I11 - a
// machine without KVM falls back - but a build that *asked* for a VM and got
// namespaces has a weaker boundary than it believes it has, and nothing in its
// output says which one it ran under.
func TestAMicroVMThatCannotBeHadIsRefused(t *testing.T) { // not parallel: sets the environment
	t.Setenv(envVM, "1")
	t.Setenv("EARTH_VM_KERNEL", "")
	t.Setenv("EARTH_VM_INITRD", "")

	_, err := sandbox("")
	if err == nil {
		t.Fatal("a microVM was asked for, could not be had, and nothing said so")
	}
}

func vmArtefact(t *testing.T, name string) string {
	t.Helper()

	at := t.TempDir() + "/" + name

	if err := os.WriteFile(at, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	return at
}
