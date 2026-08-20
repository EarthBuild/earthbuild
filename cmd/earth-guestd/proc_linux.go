//go:build linux

package main

import (
	"fmt"
	"os"

	"github.com/EarthBuild/earthbuild/engine/trace"
)

// procForTracing makes sure pid lookups resolve in this process's own namespace.
//
// The guest is pid 1 of a pid namespace, and the `/proc` it inherits is the
// host's. The two disagree silently: a seccomp notification's pid is in the
// guest's namespace, `/proc/<pid>` resolves against whatever procfs is mounted,
// and a foreign one turns every pid into a different process - EACCES on the
// numbers that exist and ENOENT on the ones that do not (E216).
//
// A private mount rather than remounting `/proc`, because `/proc` is what the
// rest of the engine and every step sees, and this is a detail of one of them.
//
// Reported and not fatal. A guest that cannot mount one still runs every step
// correctly; what it loses is the ability to observe a RUN, which is a tier and
// not a build.
func procForTracing() {
	ours, err := trace.ProcIsOurs("/proc")
	if err == nil && ours {
		return
	}

	dir, err := mountScratch("earth-proc", trace.MountPrivateProc)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"earth-guestd: no procfs of this namespace, so RUN steps will not"+
				" be observed: %v\n", err)

		return
	}

	// The third exit, and the one the seam does not cover: the mount worked and
	// is the wrong namespace's. The directory is ours to take away, and the
	// mount on it is ours to undo first - an `os.RemoveAll` over a live mount
	// removes nothing and says so.
	ours, err = trace.ProcIsOurs(dir)
	if err != nil || !ours {
		fmt.Fprintf(os.Stderr,
			"earth-guestd: the procfs mounted at %s is not this namespace's"+
				" either (%v), so RUN steps will not be observed\n", dir, err)

		if umountErr := trace.UnmountProc(dir); umountErr == nil {
			_ = os.RemoveAll(dir)
		}

		return
	}

	trace.UseProcAt(dir)
}
