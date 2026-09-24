//go:build linux

package cli

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// A caller with a store of its own gets the same choice a build gets, into that
// store.
//
// **One chooser, because there is one question.** A fleet worker ran steps for
// other people and always chose the namespace backend - not by decision, but
// because `workerSandbox` constructed `NewNative` directly and no one had
// written the choice down twice. The machine that most wants a hypervisor
// between a build and the host was the one machine that could not have one.
func TestACallerWithItsOwnStoreStillGetsTheChoice(t *testing.T) { // not parallel: sets the environment
	t.Setenv(envVM, "0")

	root := t.TempDir()

	sb, err := SandboxIn(root)
	if err != nil {
		// The namespace backend needs its agent on PATH, which a bare test
		// environment has no reason to have.
		t.Skip("no namespace sandbox on this machine: ", err)
	}

	native, ok := sb.(*exec.Native)
	if !ok {
		t.Fatalf("declining a microVM gave %T", sb)
	}

	// Its own store, not the invoking user's cache: a worker keeps layers for
	// the fleet and must not write them where a local build would.
	if native.Root != root {
		t.Errorf("the sandbox stores in %q, not the %q it was given", native.Root, root)
	}
}

// And asked for a microVM, it gets one there too.
func TestAWorkersMicroVMStoresWhereItWasTold(t *testing.T) { // not parallel: sets the environment
	t.Setenv(envVM, "1")
	t.Setenv("EARTH_VM_KERNEL", vmArtefact(t, "vmlinux"))
	t.Setenv("EARTH_VM_INITRD", vmArtefact(t, "initrd.cpio.gz"))

	root := t.TempDir()

	sb, err := SandboxIn(root)
	if err != nil {
		t.Skip("no microVM on this machine: ", err)
	}

	fc, ok := sb.(*exec.Firecracker)
	if !ok {
		t.Fatalf("asked for a microVM and got %T", sb)
	}

	if fc.StoreDir() != root {
		t.Errorf("the machine stores in %q, not the %q it was given", fc.StoreDir(), root)
	}
}

// An empty root is a build, which keeps the store where builds keep it.
func TestNoRootMeansTheUsualStore(t *testing.T) { // not parallel: sets the environment
	t.Setenv(envVM, "0")

	sb, err := SandboxIn("")
	if err != nil {
		t.Skip("no namespace sandbox on this machine: ", err)
	}

	native, ok := sb.(*exec.Native)
	if !ok {
		t.Fatalf("got %T", sb)
	}

	want, err := storeDir()
	if err != nil {
		t.Fatal(err)
	}

	if native.Root != want {
		t.Errorf("stored in %q, wanted the usual %q", native.Root, want)
	}
}
