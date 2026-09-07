package cli

import (
	"fmt"
	"os"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/guest"
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

	impliedByVM()

	return sb, nil
}

// impliedByVM turns on what a microVM leaves no choice about.
//
// **The store is on the guest's device because there is nowhere else**: the
// host cannot write a block device the guest has mounted, so the guest has to
// unpack too. Both were already settings, so asking for a VM meant spelling out
// three environment variables of which one is a fact and two are its
// consequences - and getting either consequence wrong fails at the first
// `FROM`, in the guest, saying the store holds no layer.
//
// **Only where nothing was said**, and an empty value is not an answer -
// `StoreInVM` reads `""` as "no answer" and so does this, because a setting
// that means one thing where it is written and another where it is read is the
// divergence this engine keeps finding. They are switches, and a switch that
// cannot be turned off is not one: `EARTH_STORE_IN_VM=0` is how a build asks
// whether the store is what broke it, and this must not answer over the top of
// it.
//
// Through the environment rather than through the sandbox, because that is
// where the two are read from - by this process and, for one of them, by the
// agent. A field here would be a second answer to a question that already has
// one, and the two would disagree the first time either moved.
func impliedByVM() {
	for _, name := range []string{guest.EnvStoreInVM, exec.EnvUnpackInGuest} {
		if os.Getenv(name) == "" {
			_ = os.Setenv(name, "1")
		}
	}
}
