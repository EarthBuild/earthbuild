//go:build darwin

package exec_test

import (
	"context"
	osexec "os/exec"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A VM that is merely stopped is this build's VM asleep, and is woken rather
// than replaced.
//
// The idle timeout stops an unattended sandbox after 30 minutes, so the first
// build after lunch always finds one. That used to cost a `container run` that
// fails on the name, an `rm -f`, and a full boot - 953ms measured, against 592ms
// to restart the one already there (E524).
func TestAStoppedSandboxIsResumed(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	sb := exec.NewApple()
	sb.GuestBinary = buildGuestd(t)

	err := sb.Available()
	if err != nil {
		t.Skipf("apple container backend unavailable: %v", err)
	}

	defer func() { _ = sb.Remove() }()

	base := putProbeLayerAt(t, sb.StoreDir())

	// A fresh engine each time, because that is the case being tested: the idle
	// timeout stops the VM after the process that made it has gone, and it is
	// the *next* build that finds one stopped. Stopping a VM under a live engine
	// only breaks the connection it is already holding, which is a different
	// thing and not this one.
	step := func(name string) {
		t.Helper()

		e, err := exec.New(sb)
		if err != nil {
			t.Fatalf("engine for step %s: %v", name, err)
		}

		defer e.Close()

		n := guestStep(name, "/probe")
		n.Inputs = []*ir.Node{base}

		_, err = e.Run(context.Background(), n, core.Worker{ID: "vm"}, []ir.NodeID{base.ID()}, nil)
		if err != nil {
			t.Fatalf("step %s: %v", name, err)
		}
	}

	step("before")

	if got := sb.Resumes(); got != 0 {
		t.Fatalf("a first build resumed %d VMs, want 0 - it had none to resume", got)
	}

	// Stopped from outside, which is what the idle timeout does from inside.
	//
	// The command's exit status is not the question - `container stop` takes
	// about 5s on an idle machine and answers with an XPC timeout on a busy one,
	// having stopped the VM anyway. So the state is polled instead, and a VM
	// that will not stop is a machine this test cannot ask its question on
	// rather than a failure of the engine.
	_ = osexec.Command("container", "stop", sb.Name()).Run()

	stopped := false

	for range 30 {
		out, err := osexec.Command("container", "ls", "-a").Output()
		if err == nil && exec.ParseContainers(out)[sb.Name()] == "stopped" {
			stopped = true

			break
		}

		time.Sleep(time.Second)
	}

	if !stopped {
		t.Skipf("the sandbox would not stop, so there is no stopped VM to resume")
	}

	step("after")

	if got := sb.Resumes(); got != 1 {
		t.Errorf("a stopped VM was resumed %d times, want 1 - it was rebuilt instead", got)
	}

	if got := sb.Boots(); got != 1 {
		t.Errorf("booted %d VMs, want the 1 from before the stop", got)
	}
}
