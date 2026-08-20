package exec_test

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// guestStep names a path inside the guest.
//
// Deliberately not resolved through the host's PATH, as the local backend's
// steps are: a step's filesystem is its layer stack, so a host-resolved path
// names a binary that is not in it. Resolving on the wrong side of the boundary
// is the class of bug these backends invite.
func guestStep(name, argv string) *ir.Node {
	return &ir.Node{
		Op:   ir.Op{Kind: ir.OpExec, Args: []string{argv}},
		Meta: ir.Meta{Source: "./Earthfile:" + name},
	}
}

// putProbeLayerAt places the probe binary in a layer store and returns the node
// that names it.
//
// The binary comes from EARTH_TEST_PROBE when set, and is built otherwise. The
// environment variable exists because these tests also run inside a stripped
// container with no Go toolchain, where building is not an option and skipping
// silently would hide the backend from CI entirely.
func putProbeLayerAt(t *testing.T, store string) *ir.Node {
	t.Helper()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"probe-base"}}}

	dir := filepath.Join(store, "layers", n.ID().String())
	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "probe")

	if pre := os.Getenv("EARTH_TEST_PROBE"); pre != "" {
		b, err := os.ReadFile(pre) //nolint:gosec // a test fixture path
		if err != nil {
			t.Fatalf("read the prebuilt probe at %s: %v", pre, err)
		}

		err = os.WriteFile(dst, b, 0o755) //nolint:gosec // must be executable
		if err != nil {
			t.Fatal(err)
		}

		return n
	}

	_, err = osexec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain and EARTH_TEST_PROBE is unset, so the probe cannot be provided")
	}

	build := osexec.Command("go", "build", "-o", dst,
		"github.com/EarthBuild/earthbuild/engine/exec/testdata/probe")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+probeArch(), "CGO_ENABLED=0")

	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build the probe for linux/%s: %v: %s", probeArch(), err, out)
	}

	return n
}

// probeArch is the architecture the probe must run on: the guest's, which off
// Linux is the VM's and on Linux is this machine's.
func probeArch() string {
	if a := os.Getenv("EARTH_GUEST_ARCH"); a != "" {
		return a
	}

	if runtime.GOOS == "linux" {
		return runtime.GOARCH
	}

	return testArch
}
