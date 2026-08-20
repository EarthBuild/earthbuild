package guest

import (
	"fmt"
	"os"
	"syscall"
)

// RunDaemonShimIfAsked turns this process into a daemon shim when its argv says
// so, and never returns if it does.
//
// Called first thing in `main`, by every binary that can host a guest, and from
// the test binary's `TestMain` - without that the launch re-executes the tests
// instead of a daemon, the child exits at once, and every assertion about
// stopping it passes while measuring an absence (E374).
//
// The shim exists because `dockerd` needs two things a build's user does not
// have: to be root, and a writable `/run` - `--exec-root` does not cover the
// plugin manager, which uses `/run/docker/plugins` and nothing else (E373). Go
// cannot run code between clone and exec, so the namespace is entered by
// re-executing this binary with the flags on `SysProcAttr`, and the preparation
// is done here, in the child, before the daemon replaces it.
func RunDaemonShimIfAsked() {
	if len(os.Args) < 3 || os.Args[1] != daemonShimFlag {
		return
	}

	if err := prepareShim(); err != nil {
		fmt.Fprintf(os.Stderr, "earthbuild daemon shim: %v\n", err)
		os.Exit(1)
	}

	// Exec, not run: the daemon becomes this process, so the parent's Wait sees
	// the daemon's own exit and a signal reaches the daemon rather than a
	// wrapper that would have to forward it.
	err := syscall.Exec(os.Args[2], os.Args[2:], os.Environ())

	fmt.Fprintf(os.Stderr, "earthbuild daemon shim: exec %s: %v\n", os.Args[2], err)
	os.Exit(1)
}
