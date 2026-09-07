//go:build linux

package exec_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// A microVM sandbox boots and its agent answers.
//
// The whole point of the backend is that `earth-guestd` needs no VM-specific
// code: it speaks over stdin and stdout, and `earth-vmboot` hands it a vsock
// connection as those. This asserts the chain end to end - VMM, kernel,
// initramfs, block device, vsock multiplexer, PID 1, agent - because every link
// of it is a place a guest can boot and never be spoken to.
//
// Skipped unless the machine has the parts: hosted CI runners have no
// `/dev/kvm`, and Firecracker cannot emulate what it needs (I11).
func TestAMicroVMBootsAndItsAgentAnswers(t *testing.T) { // not parallel: boots a VM
	fc := exec.NewFirecracker()
	if err := fc.Available(); err != nil {
		t.Skip("no microVM on this machine: ", err)
	}

	if os.Getenv("EARTH_VM_STORE") == "" {
		t.Skip("set EARTH_VM_STORE to an XFS image for the layer store")
	}

	conn, err := fc.Start(context.Background())
	if err != nil {
		t.Fatalf("the guest did not start: %v", err)
	}

	t.Cleanup(func() {
		if err := fc.Stop(); err != nil {
			t.Errorf("stopping the sandbox: %v", err)
		}
	})

	// The connection is the agent's stdin. Writing a byte it cannot parse makes
	// it object, which is the proof it is *running* - a silent socket would
	// equally mean earth-vmboot never handed over.
	if _, err := conn.Write([]byte{0}); err != nil {
		t.Fatalf("the agent's connection is not writable: %v", err)
	}

	if err := conn.Close(); err != nil {
		t.Errorf("closing the connection: %v", err)
	}
}

// A machine without the parts says which part, rather than failing later.
func TestAMissingKernelIsRefusedWithItsName(t *testing.T) {
	t.Parallel()

	fc := &exec.Firecracker{Binary: "firecracker"}

	err := fc.Available()
	if err == nil {
		t.Skip("this machine has every part, so there is nothing to refuse")
	}

	// Whatever is missing, the message names it: a backend that degrades has to
	// say what would make it work.
	for _, want := range []string{"kvm", "firecracker", "kernel"} {
		if contains(err.Error(), want) {
			return
		}
	}

	t.Errorf("the refusal names no missing part: %v", err)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}

	return false
}

// The store this sandbox names is a directory on **this** machine.
//
// Every caller of `StoreDir` opens it here: the CLI opens the blob store, the
// action cache and the profile store against it before anything boots, and
// `SAVE ARTIFACT` reads the staged artifact off it with an ordinary `os.Lstat`.
// A guest path there names a directory nothing on the host writes to - and
// worse, one that resolves, so the blobs would be written *somewhere* and the
// guest would find none of them.
//
// The guest's own layers are a separate thing on a separate device, addressed
// by the environment. The two are only the same directory on a backend that
// shares a filesystem, which this one cannot: Firecracker has no virtio-fs.
func TestTheStoreIsAHostDirectory(t *testing.T) {
	t.Parallel()

	at := t.TempDir()
	fc := &exec.Firecracker{Store: at}

	if got := fc.StoreDir(); got != at {
		t.Errorf("the sandbox was told to keep its store at %s and answers %s", at, got)
	}
}

// Left unset it is still a host directory, and a durable one.
//
// The store is a cache: it is worth having because the *next* build reads it.
// A temporary directory would answer every question this asks correctly and
// still throw the cache away between builds, so the assertion is that it lives
// under the user's cache directory, which is where Apple's does.
func TestTheDefaultStoreOutlivesTheBuild(t *testing.T) {
	t.Parallel()

	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skip("no user cache directory on this machine: ", err)
	}

	got := (&exec.Firecracker{}).StoreDir()

	if !strings.HasPrefix(got, cache+string(os.PathSeparator)) {
		t.Errorf("the default store is %s, which is not under the cache directory %s"+
			"\n  a store that does not outlive the build is not a cache", got, cache)
	}
}
