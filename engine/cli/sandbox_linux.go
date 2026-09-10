package cli

import (
	"fmt"
	"os"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/guest"
)

// envVM chooses whether the guest runs inside a microVM or in namespaces.
//
// **Taken by default now, where a machine can be built.** This was offered
// rather than imposed, and the reason was cost: choosing a VM whenever one was
// available would change how long the first build waits and where the layers
// live, on every machine, without being asked. The build this repository does
// most often ran at 2.93x the namespace backend, most of that a machine booted
// and taken apart again for one build.
//
// It is 1.13x now, and the machine is kept between builds. Step overhead is
// slightly *cheaper* in a guest, file access is at parity, and a compile is 6%
// off. What is bought is a boundary an escape has to cross a hypervisor to
// leave, on the engine that runs other people's Earthfiles.
//
// Set to `0` to decline. That is the way back for a machine that cannot run a
// guest and the first thing to try when asking whether the sandbox is what
// broke a build.
const envVM = "EARTH_VM"

// sandbox picks the backend for this platform: a microVM where one can be
// built, and otherwise the guest as a child process confined with namespaces
// and cgroups.
func sandbox(_ string) (exec.Sandbox, error) {
	if wantsVM() {
		sb, err := microVM()
		if err == nil {
			return sb, nil
		}

		// **Refused where it was asked for, degraded where it was assumed.** A
		// build that asked for a microVM and got namespaces runs under a
		// weaker boundary than it believes it has, and nothing in its output
		// says which it got. A build that said nothing asked for a working
		// build, and gets one.
		if askedForVM() {
			return nil, fmt.Errorf("%w"+
				"\n  set %s=0 to build in namespaces instead", err, envVM)
		}

		sayNoMicroVM(err)
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

// wantsVM reports whether this build should try for a microVM. Silence is yes.
func wantsVM() bool {
	switch os.Getenv(envVM) {
	case "0", "false", "no":
		return false
	default:
		return true
	}
}

// askedForVM reports whether somebody said so, as opposed to not saying.
//
// The two want opposite treatment when a machine cannot be built, which is why
// they are two questions: an author who asked is refused, and one who said
// nothing is told and carries on.
func askedForVM() bool { return os.Getenv(envVM) != "" && wantsVM() }

// sayNoMicroVM reports the boundary this build did not get.
//
// **Said, because the whole point is what a step can reach.** A build silently
// dropped to the namespace backend is one whose author believes their steps are
// behind a hypervisor when they are behind a kernel they share. That is the
// thing this backend exists for, so its absence is not a detail.
//
// Once and short, with the way to stop being told: this prints on every machine
// that has not built the guest artefacts, which is most of them until they
// ship.
func sayNoMicroVM(why error) {
	fmt.Fprintf(os.Stderr, "earthbuild: this build is running in namespaces"+
		" rather than a microVM: %v\n"+
		"  set %s=0 to choose that deliberately and stop being told\n", why, envVM)
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
		// **Neutral about who asked**, because both callers use this and they
		// asked different questions. One is a build that named the backend and
		// is about to be refused; the other said nothing and is about to be
		// told it got namespaces. A message asserting "you asked for this"
		// reads as a lie to the second, and told one for a while.
		return nil, fmt.Errorf("this machine cannot run a microVM: %w", err)
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
