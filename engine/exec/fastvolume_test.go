//go:build darwin

package exec

import (
	"slices"
	"strings"
	"testing"
)

// A sandbox gets storage the guest owns, and it is not the host share.
//
// Measured in the same guest, 4,000 files: untarring into an ext4 volume takes
// 0.09s and into a virtiofs share 2.31s; removing the tree takes 0.00s against
// 0.62s. Metadata operations on a block device never leave the guest kernel,
// where every one of them over a share is a round trip across the VM boundary.
//
// This is where a cache mount belongs: it must outlive the build, which is why
// it was on the shared store, and it does not need the *host* to see it.
func TestASandboxIsGivenAVolumeItOwns(t *testing.T) {
	t.Parallel()

	a := &Apple{Image: "alpine:3.22", Store: "/tmp/store", dir: "/tmp/ctx", name: "earthbuild-abc123"}

	args := a.runArgs()

	if !slices.Contains(args, "-v") {
		t.Fatalf("no mounts at all: %v", args)
	}

	joined := strings.Join(args, " ")

	if !strings.Contains(joined, a.volumeName()+":"+guestFast) {
		t.Errorf("the sandbox is not given its volume\n  args: %s", joined)
	}
}

// The volume is this sandbox's, not everybody's.
//
// **Two VMs attaching one volume writably corrupts it**, and the framework
// offers no lock to prevent that - it is silent. Sandboxes are already named by
// what makes them different, so a volume named after its sandbox is attached by
// exactly one VM by construction.
func TestTheVolumeBelongsToOneSandbox(t *testing.T) {
	t.Parallel()

	one := (&Apple{name: "earthbuild-aaa"}).volumeName()
	two := (&Apple{name: "earthbuild-bbb"}).volumeName()

	if one == two {
		t.Errorf("two sandboxes share the volume %q, which corrupts it silently", one)
	}

	if !strings.Contains(one, "earthbuild-aaa") {
		t.Errorf("volume %q is not named after its sandbox, so the pairing is not obvious", one)
	}
}
