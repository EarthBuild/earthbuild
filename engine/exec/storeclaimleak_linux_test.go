//go:build linux

package exec

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A Start that fails leaves the store claimable.
//
// **Because a Start that fails leaves nothing for Stop to be called on.** The
// claim is taken before the machine exists - deliberately, since it is what
// says this host is not already running a guest on that device - and every
// failure after it returned without giving it back. The caller sees `start
// sandbox: ...`, has no sandbox, and never calls Stop; the claim then outlives
// the build for the whole life of the process.
//
// One corpus run in one process: 25 invocations refused with `the store device
// is in use by this build itself: a sandbox it started has not been stopped`.
func TestAFailedStartGivesTheStoreBack(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "store.img")

	err := os.WriteFile(img, make([]byte, 4096), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	f := NewFirecracker()
	f.StoreImage = img
	f.Root = dir

	// A VMM that is not there, so Start gets as far as launching and fails.
	//
	// **Without the network shim**, because with it the child is this binary -
	// which exists - and the failure lands later, on a path that already stops
	// the sandbox and gives the claim back. The paths worth testing are the
	// ones before the machine is running at all.
	// Naming a tap turns the shim off - attachNet sets ownNet itself, from the
	// environment, after the claim is taken.
	t.Setenv(EnvTap, "earth-no-such-tap")

	f.Binary = filepath.Join(dir, "no-such-firecracker")
	f.Kernel = filepath.Join(dir, "vmlinux")
	f.Initrd = filepath.Join(dir, "initrd.cpio.gz")

	for _, at := range []string{f.Kernel, f.Initrd} {
		err = os.WriteFile(at, []byte("not a kernel"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err = f.Start(context.Background())
	if err == nil {
		t.Fatal("a sandbox started with a VMM that does not exist")
	}

	// The point: another build - or the next one in this process - can have it.
	release, err := claimStore(img)
	if err != nil {
		t.Fatalf("the store is still claimed after a Start that failed: %v"+
			"\n  the failure was: %v", err, err)
	}

	release()
}
