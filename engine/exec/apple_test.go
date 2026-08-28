//go:build darwin

package exec_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestAppleSandboxConfinesAndCaptures is the first point in the engine where a
// result becomes cacheable: the step runs inside a VM, so A3 holds, so the
// capture is a claim other builds may trust.
//
// Everything before this was correct and uncacheable by construction.
func TestAppleSandboxConfinesAndCaptures(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	sharedStore(t)
	sb := exec.NewApple()
	sb.GuestBinary = buildGuestd(t)

	err := sb.Available()
	if err != nil {
		t.Skipf("apple container backend unavailable: %v", err)
	}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	// The VM outlives Close by design, so a test that made one with a name
	// nothing else will ever use has to take it away. Without this the suite
	// leaves a 1GB VM behind per run.
	defer func() { _ = sb.Remove() }()

	if !sb.Confines() {
		t.Fatal("a VM backend that does not confine is not a VM backend")
	}

	// A step's filesystem is its layer stack, not the sandbox image, so the
	// binary to run must be placed in a layer first. This is also why an empty
	// stack cannot run /bin/true: there is no /bin.
	base := putProbeLayerAt(t, sb.StoreDir())

	n := guestStep("1", "/probe")
	n.Inputs = []*ir.Node{base}

	res, err := e.Run(context.Background(), n, core.Worker{ID: "vm"}, []ir.NodeID{base.ID()}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if res.Exit != 0 {
		t.Errorf("probe exited %d: %s", res.Exit, res.Output)
	}

	if !res.Captured {
		t.Error("a confined step produced an uncaptured result")
	}

	if res.Layer == (ir.NodeID{}) {
		t.Error("captured result carries no layer digest")
	}
}

// One VM serves the whole run. At ~650ms to boot against ~60ms to exec -
// measured on this machine, matching experiment E1b - a VM per step would spend
// more time booting than building.
func TestAppleSandboxBootsOnce(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	sharedStore(t)
	sb := exec.NewApple()
	sb.GuestBinary = buildGuestd(t)

	err := sb.Available()
	if err != nil {
		t.Skipf("apple container backend unavailable: %v", err)
	}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	// The VM outlives Close by design, so a test that made one with a name
	// nothing else will ever use has to take it away. Without this the suite
	// leaves a 1GB VM behind per run.
	defer func() { _ = sb.Remove() }()

	base := putProbeLayerAt(t, sb.StoreDir())

	for _, name := range []string{"a", "b", "c"} {
		n := guestStep(name, "/probe")
		n.Inputs = []*ir.Node{base}

		_, err := e.Run(context.Background(), n, core.Worker{ID: "vm"}, []ir.NodeID{base.ID()}, nil)
		if err != nil {
			t.Fatalf("step %s: %v", name, err)
		}
	}

	if got := sb.Boots(); got != 1 {
		t.Errorf("3 steps booted %d VMs, want 1", got)
	}
}

// TestFromAlpineRunTrue is the shape of an actual Earthfile: FROM a real base
// image, RUN a real command from it.
//
// Everything is real - the registry pull, the digest verification, the unpack,
// the VM, the chroot, the capture. It is the first point at which the native
// engine does what a build tool does.
func TestFromAlpineRunTrue(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	sb := exec.NewApple()
	sb.GuestBinary = buildGuestd(t)

	err := sb.Available()
	if err != nil {
		t.Skipf("apple container backend unavailable: %v", err)
	}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	// The VM outlives Close by design, so a test that made one with a name
	// nothing else will ever use has to take it away. Without this the suite
	// leaves a 1GB VM behind per run.
	defer func() { _ = sb.Remove() }()

	// FROM alpine:3.22
	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"alpine:3.22"}}}

	// StoreDir, not the Store field: the field is the override and is empty
	// until something resolves it, and pulling into "" put the image in the
	// working directory while the VM looked in its own store.
	dir := filepath.Join(sb.StoreDir(), "layers", base.ID().String())
	_, err = image.Pull(t.Context(), "alpine:3.22", dir, image.Options{Platform: testPlatform})
	if err != nil {
		t.Fatal(err)
	}

	// RUN /bin/busybox true
	n := guestStep("run", "/bin/busybox")
	n.Op.Args = []string{"/bin/busybox", "true"}
	n.Inputs = []*ir.Node{base}

	res, err := e.Run(t.Context(), n, core.Worker{ID: "vm"}, []ir.NodeID{base.ID()}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if res.Exit != 0 {
		t.Fatalf("busybox true exited %d: %s", res.Exit, res.Output)
	}

	if !res.Captured {
		t.Error("a step over a real base image was not captured")
	}
}

// A sandbox resolves its store to somewhere real before anything writes there.
//
// `Store` is the override and is empty until something resolves it; `StoreDir`
// is the answer. A test that pulled into the field instead unpacked an entire
// alpine root filesystem into the *working directory* - which for a test is the
// package under test, so `engine/exec/layers/` appeared in the repository and
// was staged with 400 files before anyone noticed.
func TestASandboxStoreIsAnAbsolutePath(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	sb := exec.NewApple()
	sb.GuestBinary = buildGuestd(t)

	dir := sb.StoreDir()
	if dir == "" {
		t.Fatal("the store resolved to nothing, so anything written to it lands in the working directory")
	}

	if !filepath.IsAbs(dir) {
		t.Errorf("the store is %q, which is relative to wherever the process happens to be", dir)
	}
}
