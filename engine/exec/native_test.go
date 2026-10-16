//go:build linux

package exec_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Unprivileged is not the question; a user namespace is.
//
// This test used to assert the opposite, and it was right when it was written:
// overlayfs needs CAP_SYS_ADMIN (E13), so without privilege the backend could
// not assemble a layer stack and refusal (I10) was the honest answer rather
// than degradation (I11).
//
// **What changed is a measurement, not an opinion.** On a 6.12 kernel:
//
//	unshare -Umr sh -c "mount -t overlay ... && rm m/a"
//	MOUNTED
//	c--------- 2 root root 0, 0 a
//
// An unprivileged user mounted an overlay and `rm` wrote a whiteout into it.
// The capability is checked *in the namespace the mount happens in*, and a user
// namespace grants it there while granting nothing on the host - which is how
// every rootless container runtime works.
//
// So the refusal now turns on whether a user namespace can be made. Where one
// can, the backend is available and a build runs; where a distribution has
// disabled them, it still refuses and now names that instead of the euid, which
// is the thing a reader can act on.
func TestTheNativeBackendTurnsOnUserNamespaces(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("running as root, so the unprivileged path cannot be reached")
	}

	err := exec.NewNative().Available()

	// A machine that will make one must not be turned away *for being
	// unprivileged*. It may still be turned away for something else - a missing
	// `earth-guestd` is the ordinary case in a test environment, and asserting
	// `err == nil` here conflated "not refused for privilege" with "everything
	// else is in place", which is how the first version of this failed on the
	// very machine that proved the feature works.
	_, statErr := os.Stat("/proc/self/ns/user")
	if statErr == nil {
		if err != nil && strings.Contains(err.Error(), "euid") {
			t.Errorf("a machine with user namespaces was refused for being unprivileged:\n%s", err)
		}

		return
	}

	if err == nil {
		t.Fatal("the backend claimed to be available with no user namespaces to be had")
	}

	// And where it cannot, the refusal names what to check rather than who is
	// asking: "you are not root" is not actionable when being root is not the
	// requirement.
	for _, want := range []string{"user namespace", "buildkit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q:\n%s", want, err)
		}
	}
}

// The Linux backend is the second implementation of Sandbox, and the point at
// which the port finds out whether it was honest. It differs from macOS in that
// there is no VM: "boot" is a subprocess and costs microseconds rather than the
// ~650ms a VM needs.
func TestNativeSandboxConfinesWhenItCan(t *testing.T) {
	t.Parallel()

	sb := exec.NewNative()
	err := sb.Available()
	if err != nil {
		t.Skipf("native backend unavailable: %v", err)
	}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	// Available implies confining here: the backend refuses rather than running
	// unconfined, so there is no state in which it works and does not confine.
	if !sb.Confines() {
		t.Error("the native backend is available but reports that it does not confine")
	}

	base := putProbeLayerAt(t, sb.StoreDir())

	n := guestStep("1", "/probe")
	n.Inputs = []*ir.Node{base}

	res, err := e.Run(context.Background(), n, core.Worker{ID: testNative}, []ir.NodeID{base.ID()}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if res.Exit != 0 {
		t.Fatalf("probe exited %d: %s", res.Exit, res.Output)
	}

	// Captured tracks confinement, not merely whether a digest was computed.
	if res.Captured != sb.Confines() {
		t.Errorf("Captured = %v but Confines = %v; a result is cacheable only when both hold",
			res.Captured, sb.Confines())
	}
}

// One guest serves the whole run here too, though for a different reason: not
// boot cost, but that a second guest would hold a second set of mounts.
func TestNativeSandboxStartsOneGuest(t *testing.T) {
	t.Parallel()

	sb := exec.NewNative()
	err := sb.Available()
	if err != nil {
		t.Skipf("native backend unavailable: %v", err)
	}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	base := putProbeLayerAt(t, sb.StoreDir())

	for _, name := range []string{"a", "b", "c"} {
		n := guestStep(name, "/probe")
		n.Inputs = []*ir.Node{base}

		_, err := e.Run(context.Background(), n, core.Worker{ID: testNative}, []ir.NodeID{base.ID()}, nil)
		if err != nil {
			t.Fatalf("step %s: %v", name, err)
		}
	}

	if got := sb.Boots(); got != 1 {
		t.Errorf("3 steps started %d guests, want 1", got)
	}
}
