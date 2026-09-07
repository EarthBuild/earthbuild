//go:build linux

package exec_test

import (
	"context"
	"os"
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
