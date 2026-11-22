//go:build linux

package exec

import (
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/cmd/earth-vmboot/vmboot"
)

// The microVM can answer a fault-in, so a worker running in one still primes.
//
// Without it a worker gets whole layers instead of the paths a step was
// predicted to read - correct, and the reason the fleet's laziest transfer was
// unavailable on the backend a worker most wants to use.
func TestTheMicroVMCanAnswerAFaultIn(t *testing.T) {
	t.Parallel()

	var sb Sandbox = &Firecracker{}

	filler, ok := sb.(interface {
		SetFill(func(handle, path string) error)
	})
	if !ok {
		t.Fatal("the microVM cannot be given a fault-in channel, so a worker" +
			" running in one takes whole layers")
	}

	// **Setting it must not deadlock, and must not need a machine.** `Start`
	// holds the lock for its whole body and `attach` runs inside it, so the
	// server has a locked and an unlocked entry point; a version that locked in
	// both places hung the build with nothing to read, which is the failure
	// `attach` already carries a comment about.
	done := make(chan struct{})

	go func() {
		defer close(done)

		filler.SetFill(func(string, string) error { return nil })
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SetFill did not return: the fault-in server is taking a lock" +
			" one of its callers already holds")
	}
}

// Nothing is served until there is a machine to serve it over.
//
// A sandbox that dialled on being told about the callback would dial an address
// that does not exist yet, and report a machine that cannot fault in when it
// simply has not started.
func TestNoMachineMeansNoFaultInServerYet(t *testing.T) {
	t.Parallel()

	f := &Firecracker{}
	f.SetFill(func(string, string) error { return nil })

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.fills {
		t.Error("a fault-in server started against a machine that is not running")
	}

	if f.fill == nil {
		t.Error("the callback was not kept for when a machine appears")
	}
}

// The port is its own, since it carries the one exchange that runs the other
// way and shares a device with nothing.
func TestTheFaultInPortIsDistinct(t *testing.T) {
	t.Parallel()

	seen := map[int]string{
		vmboot.VsockPort:  "protocol",
		vmboot.BulkPort:   "bulk",
		vmboot.ExportPort: "export",
	}

	if what, clash := seen[vmboot.FillPort]; clash {
		t.Errorf("the fault-in port is also the %s port (%d)", what, vmboot.FillPort)
	}
}
