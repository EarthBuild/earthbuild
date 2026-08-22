//go:build darwin

package exec_test

import (
	"context"
	osexec "os/exec"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Removing a sandbox removes its storage too.
//
// The VM's fast storage is a volume, and `container rm` does not touch volumes -
// which is right for a VM that stops and comes back, and wrong for one being
// taken away. Every VM-booting test names a sandbox after a temporary guest
// directory, so each one minted a volume nothing would ever name again: 11 of
// them, holding 14GB, accumulated in an hour of running this suite (E526).
func TestRemovingASandboxRemovesItsVolume(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	sb := exec.NewApple()
	sb.GuestBinary = buildGuestd(t)

	if err := sb.Available(); err != nil {
		t.Skipf("apple container backend unavailable: %v", err)
	}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	base := putProbeLayerAt(t, sb.StoreDir())

	n := guestStep("probe", "/probe")
	n.Inputs = []*ir.Node{base}

	if _, err := e.Run(context.Background(), n, core.Worker{ID: "vm"}, []ir.NodeID{base.ID()}, nil); err != nil {
		t.Fatalf("step: %v", err)
	}

	_ = e.Close()

	volume := sb.Name() + "-fast"

	if !volumeExists(t, volume) {
		t.Fatalf("%s was never created, so its removal proves nothing", volume)
	}

	if err := sb.Remove(); err != nil {
		t.Fatalf("remove the sandbox: %v", err)
	}

	if volumeExists(t, volume) {
		t.Errorf("%s outlived the sandbox it belongs to", volume)
	}
}

func volumeExists(t *testing.T, name string) bool {
	t.Helper()

	out, err := osexec.Command("container", "volume", "ls").Output()
	if err != nil {
		t.Fatalf("list volumes: %v", err)
	}

	for line := range strings.Lines(string(out)) {
		if first, _, _ := strings.Cut(strings.TrimSpace(line), " "); first == name {
			return true
		}
	}

	return false
}
