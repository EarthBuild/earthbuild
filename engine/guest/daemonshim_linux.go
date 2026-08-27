//go:build linux

package guest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// prepareShim gives the daemon the writable `/run` it will not start without.
//
// A tmpfs rather than a bind: it is thrown away with the namespace, so a daemon
// that dies badly leaves nothing on the machine, and two daemons cannot see each
// other's plugin sockets.
func prepareShim() error {
	// Read before the mount, because after it the file is not there to read.
	// See resolver.
	keep := savedResolver()

	err := unix.Mount("none", "/run", "tmpfs", 0, "")
	if err != nil {
		return fmt.Errorf("mount a private /run: %w%s", err, sysAdminHint(err))
	}

	return restoreResolver(keep)
}

// resolver is the machine's resolver configuration, and where it lives.
//
// **A private /run can hide the resolver.** On a machine using
// systemd-resolved - which is every GitHub runner - `/etc/resolv.conf` is a
// symlink into `/run/systemd/resolve/`, so the tmpfs above covers the file it
// points at. The daemon then finds no nameserver, falls back to localhost, and
// every pull fails with
//
//	lookup registry-1.docker.io on [::1]:53: read: connection refused
//
// which reads as a network problem and is a mount (E777).
type resolver struct {
	at   string
	data []byte
}

// savedResolver reads the resolver the machine is using, following the symlink
// so that what is saved is the file the tmpfs will hide rather than the link.
func savedResolver() resolver {
	at, err := filepath.EvalSymlinks("/etc/resolv.conf")
	if err != nil {
		return resolver{}
	}

	data, err := os.ReadFile(at) //nolint:gosec // the machine's own resolver
	if err != nil {
		return resolver{}
	}

	return resolver{at: at, data: data}
}

// restoreResolver puts the resolver back inside the private /run.
//
// Only there. A resolver that is a real file, or one pointing into the store as
// it does on NixOS, is untouched by the mount, and writing a copy would be this
// engine inventing a resolver nobody asked it for.
//
// Not fatal on failure: a daemon that cannot resolve is worse than one that
// can, and both are better than a step that does not run at all.
func restoreResolver(keep resolver) error {
	if !hiddenByPrivateRun(keep.at) {
		return nil
	}

	return writeResolver(keep.at, keep.data)
}

// hiddenByPrivateRun reports whether the tmpfs above covers this path.
//
// Only `/run`. A resolver that is a real file, or one pointing into the store
// as it does on NixOS, is untouched by the mount, and writing a copy would be
// this engine inventing a resolver nobody asked it for.
func hiddenByPrivateRun(at string) bool {
	return at != "" && strings.HasPrefix(at, "/run/")
}

// writeResolver puts the saved configuration back where the symlink expects it.
func writeResolver(at string, data []byte) error {
	err := os.MkdirAll(filepath.Dir(at), 0o755)
	if err != nil {
		return fmt.Errorf("make room for the resolver at %s: %w", at, err)
	}

	err = os.WriteFile(at, data, 0o644) //nolint:gosec // a resolver is world-readable
	if err != nil {
		return fmt.Errorf("put the resolver back at %s: %w", at, err)
	}

	return nil
}

// namespaced puts the shim where it can be root with a `/run` of its own.
func namespaced(a *syscall.SysProcAttr) *syscall.SysProcAttr {
	return namespacedAs(a, os.Getuid(), os.Getgid())
}

// namespacedAs is namespaced with the identity made explicit, so both branches
// can be tested from one process.
//
// **A user namespace only when one is needed.** It exists for a single reason -
// `dockerd` refuses to start unless it is root (E373) - and a guest that is
// already root has that satisfied. Asking anyway nests a namespace, and nesting
// is not free: at one level of user namespace every shape works, and adding a
// *pid* namespace breaks the inner one with `fork/exec: permission denied`,
// because the parent writes `/proc/<pid>/uid_map` through a `/proc` that does
// not match the pid namespace, so the child never receives its mapping and execs
// as nobody (E377). The guest is often already in a user namespace (E105), which
// makes this the common case rather than the exotic one.
//
// The mount namespace is asked for either way: the private `/run` is the other
// half of what the daemon needs, and root-in-a-namespace already carries the
// capability to mount one.
//
// Where a namespace *is* created, root in it maps back to the invoking user
// outside it, so the daemon's files on the guest's disk belong to whoever ran
// the build - which is what makes a named cache readable by the next one (E365).
func namespacedAs(a *syscall.SysProcAttr, uid, gid int) *syscall.SysProcAttr {
	a.Cloneflags |= syscall.CLONE_NEWNS
	a.Unshareflags |= syscall.CLONE_NEWNS

	if uid == 0 {
		return a
	}

	a.Cloneflags |= syscall.CLONE_NEWUSER
	a.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: uid, Size: 1}}
	a.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: gid, Size: 1}}

	// setgroups must be denied before a gid map can be written by an
	// unprivileged process. The kernel requires it; it is not a choice.
	a.GidMappingsEnableSetgroups = false

	return a
}
