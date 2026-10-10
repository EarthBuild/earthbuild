//go:build linux

package exec

import (
	"os"
	"syscall"
)

// unprivilegedNamespace runs a child as root inside a user namespace of its own.
//
// The single map entry is the whole trick: the invoking user becomes uid 0
// *inside*, which is where a mount's CAP_SYS_ADMIN is checked, and remains
// themselves outside, where the files land. Nothing is granted on the host.
//
// A mount namespace with it, because a user namespace alone owns no mounts to
// make - the two are always paired for this purpose.
func unprivilegedNamespace() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		// A PID namespace as well, because mounting procfs is refused for a
		// PID namespace the caller does not own - the guest got as far as
		// mounting the overlay and then failed with `mount /proc for the step:
		// operation not permitted`, which is that rule and not a missing
		// capability.
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID,
		UidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: os.Geteuid(), Size: 1},
		},
		GidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: os.Getegid(), Size: 1},
		},
		// Without this the mapping is refused unless the process has
		// CAP_SETGID, which is the thing it does not have.
		GidMappingsEnableSetgroups: false,
	}
}

// userNamespacesAvailable reports whether this machine will make one.
//
// Asked by making one, in a child that does nothing - a distribution may
// disable them outright, and a check that assumed the kernel version would be
// wrong on exactly the machines where the answer matters.
func userNamespacesAvailable() bool {
	// /proc/self/ns/user exists wherever the kernel has the feature compiled
	// in; the sysctl below is how a distribution turns it off for
	// unprivileged callers.
	_, err := os.Stat("/proc/self/ns/user")
	if err != nil {
		return false
	}

	b, err := os.ReadFile("/proc/sys/kernel/unprivileged_userns_clone")
	if err != nil {
		// No such knob on most kernels, which means unrestricted.
		return true
	}

	return len(b) > 0 && b[0] != '0'
}
