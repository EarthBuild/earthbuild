package guest

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// A step gets /sys by a bind where a fresh sysfs mount is refused.
//
// Mounting sysfs needs the network namespace to belong to the user namespace
// doing the mounting. That is true of a privileged container on a developer's
// machine and false on a GitHub runner, where every Native job reported
// `mount /sys for the step: operation not permitted` - three times each, in all
// six jobs sampled, which is why the inner buildkitd then found no cgroup mount
// and could start nothing (E839a).
//
// **A bind carries no such requirement**, because it instantiates no new sysfs
// superblock. It is content-equivalent here: sysfs is network-namespace tagged,
// this engine deliberately does not apply CLONE_NEWNET, so the step shares the
// guest's namespace and a fresh mount would show exactly what the bind shows.
//
// The mount call is injected so the refusal can be forced. The alternative is a
// test that only exercises the fallback on a machine that cannot mount sysfs,
// which is the machine nobody runs the tests on.
func TestSysFallsBackToABindWhenSysfsIsRefused(t *testing.T) {
	t.Parallel()

	t.Run("a bind is tried when the fresh mount is not permitted", func(t *testing.T) {
		t.Parallel()

		var (
			tried           []string
			readOnlyRemount bool
		)

		_, err := mountSysWith(t.TempDir(), func(source, _, fstype string, flags uintptr, _ string) error {
			tried = append(tried, fstype+" from "+source)

			if fstype == "sysfs" {
				return unix.EPERM
			}

			if flags&unix.MS_BIND == 0 {
				t.Errorf("the fallback is not a bind: flags %#x", flags)
			}

			// **Not recursive, and this is the whole difference between a
			// bind that is equivalent to a fresh sysfs and one that is not.**
			// The machine mounts cgroup2 at /sys/fs/cgroup, and MS_REC would
			// bring it along - so a step whose own cgroup mount is skipped, on
			// a cgroups v1 machine, would be shown the machine's entire
			// hierarchy. A fresh `mount -t sysfs` gives an empty directory
			// there. Ambient state a step can observe that no key describes is
			// what I3 forbids, so the bind must be shallow.
			if flags&unix.MS_REMOUNT == 0 && flags&unix.MS_REC != 0 {
				t.Errorf("the bind of /sys is recursive: it carries the machine's "+
					"own submounts, including its cgroup tree (flags %#x)", flags)
			}

			if flags&unix.MS_REMOUNT != 0 && flags&unix.MS_RDONLY != 0 {
				readOnlyRemount = true
			}

			return nil
		})
		if err != nil {
			t.Fatalf("a refused sysfs mount should fall back to a bind, got: %v", err)
		}

		// Three: the refused sysfs, the bind, and the remount that puts the
		// read-only flag back - a bind takes its source's flags, so read-only
		// has to be asserted after it rather than with it.
		if len(tried) != 3 {
			t.Fatalf("attempts were %v, want sysfs, then a bind of /sys, then a remount", tried)
		}

		if !strings.HasPrefix(tried[0], "sysfs") || !strings.HasPrefix(tried[1], "none from /sys") {
			t.Errorf("attempts were %v, want a sysfs mount then a bind of /sys", tried)
		}

		if !readOnlyRemount {
			t.Error("the bound /sys was left writable: no read-only remount followed it")
		}
	})

	t.Run("the fresh mount is preferred when it works", func(t *testing.T) {
		t.Parallel()

		var n int

		_, err := mountSysWith(t.TempDir(), func(_, _, _ string, _ uintptr, _ string) error {
			n++

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		if n != 1 {
			t.Errorf("%d mount attempts, want one: a working sysfs mount needs no fallback", n)
		}
	})

	t.Run("both failing reports both reasons", func(t *testing.T) {
		t.Parallel()

		_, err := mountSysWith(t.TempDir(), func(_, _, fstype string, _ uintptr, _ string) error {
			if fstype == "sysfs" {
				return unix.EPERM
			}

			return unix.EACCES
		})
		if err == nil {
			t.Fatal("both mounts failed and mountSys reported success")
		}

		// Both, because "operation not permitted" alone sent a reader to the
		// wrong half of this for a month.
		if !errors.Is(err, unix.EACCES) || !strings.Contains(err.Error(), "not permitted") {
			t.Errorf("the error names only one attempt: %v", err)
		}
	})
}
