package cli

import (
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// checkIsolationSupported refuses a plan this backend cannot run, before it
// starts anything.
//
// The executor refuses the same thing (E391) and that refusal is the guarantee -
// it is the last place the decision is made and it cannot be bypassed. This one
// is the courtesy: on a backend whose sandbox is a VM, the executor's refusal
// arrives after an image has been chosen, a machine booted, and a step sent to
// it, so the author waits for a boot to be told about a flag.
//
// Two checks at two boundaries, reading different things, on the same argument
// as the scheduler's cache gate (E384). Not two copies of one rule: this reads
// the plan, that reads the step, and neither is derived from the other.
func checkIsolationSupported(g *ir.Graph) error {
	if backendCanIsolate() {
		return nil
	}

	for _, n := range g.Nodes() {
		if !n.Op.IsolateDocker {
			continue
		}

		return fmt.Errorf(
			"%s: WITH DOCKER --isolate asks for a daemon of this step's own, and"+
				"\n  this backend has only the sandbox VM's, which the blocks of a"+
				"\n  build share"+
				"\n  a plain WITH DOCKER is unaffected by earlier builds here and"+
				"\n  needs no flag; a daemon per step is the native backend's:"+
				"\n  build it with the `earth-native` binary", n.Meta.Source)
	}

	return nil
}
