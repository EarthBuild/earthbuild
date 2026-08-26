//go:build linux

package guest

import (
	"fmt"
	"os"
	"syscall"
)

// RunStepShimIfAsked turns this process into a step shim when its argv says so,
// and never returns if it does.
//
// Called first thing in `main`, by every binary that can host a guest, and from
// the test binary's `TestMain` - the daemon shim learned that the hard way, when
// a launch re-executed the tests instead of a daemon and every assertion about
// stopping it passed while measuring an absence (E374).
//
// Go cannot run code between clone and exec, so the step's namespaces are
// entered by re-executing this binary with the flags on `SysProcAttr`, and the
// preparation happens here, in the child, before the step replaces it. The shim
// chroots itself rather than letting `SysProcAttr.Chroot` do it, which is what
// lets it be the guest's own binary at the guest's own path: nothing is written
// into the step's filesystem and nothing needs undoing before the layer is
// captured (E705).
func RunStepShimIfAsked() {
	sh := stepShimAsked(os.Args)
	if sh == nil {
		return
	}

	fail := func(err error) {
		fmt.Fprintf(os.Stderr, "earthbuild step shim: %v\n", err)
		os.Exit(1)
	}

	err := prepareStep(sh)
	if err != nil {
		fail(err)
	}

	err = enterStep(sh)
	if err != nil {
		fail(err)
	}

	// Exec, not run: the step becomes this process, so the guest's Wait sees the
	// step's own exit and a signal reaches the step rather than a wrapper that
	// would have to forward it. It also means the shim is gone by the time the
	// step's first instruction runs, which is what keeps it out of the step's
	// process table.
	//
	// G204: the argv is the step's, which is the whole job.
	err = syscall.Exec(sh.argv[0], sh.argv, os.Environ()) //nolint:gosec // see above

	fail(fmt.Errorf("exec %s: %w", sh.argv[0], err))
}
