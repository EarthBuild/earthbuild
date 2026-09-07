package cli

import (
	"fmt"
	"os"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// envVM asks for the guest to run inside a microVM rather than in namespaces.
//
// **Offered, not imposed.** Nearly every Linux machine has `/dev/kvm`, and
// choosing the VM whenever one is available would change what a step can reach,
// how long the first build waits and where the layers live - on every machine,
// silently. The stronger boundary is worth having and is not worth taking
// without being asked (I11).
const envVM = "EARTH_VM"

// sandbox picks the backend for this platform: the guest as a child process,
// confined with namespaces and cgroups, or inside a microVM when asked.
func sandbox(_ string) (exec.Sandbox, error) {
	if wantsVM() {
		return microVM()
	}

	dir, err := storeDir()
	if err != nil {
		return nil, err
	}

	sb := exec.NewNative()
	sb.Root = dir

	err = sb.Available()
	if err != nil {
		return nil, err
	}

	return sb, nil
}

func wantsVM() bool {
	switch os.Getenv(envVM) {
	case "", "0", "false", "no":
		return false
	default:
		return true
	}
}

// microVM is the Firecracker backend, or the reason there is not one.
//
// **Refused rather than degraded, which is the opposite of what `Available`
// does.** A machine that cannot run a VM falls back to namespaces when nothing
// asked for one; a build that *did* ask and got namespaces runs under a weaker
// boundary than it believes it has, and nothing in its output says which it
// got. So the degrade lives at the default and the refusal lives here.
func microVM() (exec.Sandbox, error) {
	sb := exec.NewFirecracker()

	err := sb.Available()
	if err != nil {
		return nil, fmt.Errorf("%s asked for a microVM and this machine cannot run one: %w"+
			"\n  unset %s to build in namespaces instead, which is the default",
			envVM, err, envVM)
	}

	return sb, nil
}
