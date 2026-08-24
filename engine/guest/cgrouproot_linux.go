//go:build linux

package guest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// EnvCgroupParent is where the host tells the guest to create step cgroups.
//
// Named once because it is written by one process and read by another, and a
// name spelled differently on the two sides is a variable nobody reads.
const EnvCgroupParent = "EARTH_GUEST_CGROUP_PARENT"

// cgroupParent is the directory to create a step's control group under.
//
// Found rather than assumed. `/sys/fs/cgroup` belongs to root, so an
// unprivileged build could never create `<root>/earthbuild` and every step ran
// unbounded (E123).
//
// cgroup v2 delegates a writable subtree to a user session, and a process may be
// **placed** only in a cgroup under a common ancestor it can write - so a
// process started inside a delegated scope can create children of its own cgroup
// and move steps into them, while one started outside cannot be moved in at all.
// Measured:
//
//	rootless, plain session        /sys/fs/cgroup/earthbuild    permission denied
//	rootless, systemd-run --scope  <own cgroup>/earthbuild      works
//	root                           /sys/fs/cgroup/earthbuild    works
//
// So: this process's own cgroup when it can write there, the root otherwise.
func cgroupParent() (string, bool) {
	// Where the host says, when it says. The host takes over the delegated
	// scope and moves *itself* into a leaf of it (TakeOverCgroup), so the guest
	// - started afterwards, inheriting that leaf - would otherwise put step
	// cgroups underneath the host's own, where the host is a process and the
	// controllers can never be enabled.
	//
	// The guest cannot work this out for itself: `/proc/self/cgroup` shows it
	// the leaf, and the scope above it is the host's knowledge (E124). So the
	// host passes it, the way it passes every other thing the guest cannot see.
	if p := os.Getenv(EnvCgroupParent); p != "" && writableDir(p) {
		return p, true
	}

	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return cgroupParentIn(cgroupRoot, "")
	}

	return cgroupParentIn(cgroupRoot, string(b))
}

// cgroupParentIn is cgroupParent against a given root and /proc/self/cgroup
// body, so the choice can be tested without being root and without a cgroup
// filesystem.
func cgroupParentIn(root, selfCgroup string) (string, bool) {
	if own, ok := ownCgroupPath(selfCgroup); ok {
		p := filepath.Join(root, own)
		if writableDir(p) {
			return p, true
		}
	}

	if writableDir(root) {
		return root, true
	}

	return "", false
}

// ownCgroupPath is the path from a cgroup v2 line: `0::/user.slice/…`.
//
// v2 only. A v1 machine has one line per controller and no unified hierarchy to
// delegate, so there is nothing here to find and the root is the only candidate
// - which is what returning false selects.
func ownCgroupPath(body string) (string, bool) {
	for line := range strings.SplitSeq(strings.TrimSpace(body), "\n") {
		rest, ok := strings.CutPrefix(line, "0::")
		if !ok {
			continue
		}

		rest = strings.TrimSpace(rest)
		if rest == "" || rest == "/" || !strings.HasPrefix(rest, "/") {
			return "", false
		}

		return rest, true
	}

	return "", false
}

// writableDir reports whether this process may create entries in a directory.
//
// `unix.Access` with W_OK|X_OK, which is the question - not the mode bits and
// not the owner. A directory owned by somebody else with a group this process
// belongs to is writable, and one owned by this process on a read-only mount is
// not; deciding from `Stat` would get both wrong.
//
// **Nothing is created by asking.** A probe that made the directory to find out
// would leave one behind on every machine it decided against.
func writableDir(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || !fi.IsDir() {
		return false
	}

	return unix.Access(p, unix.W_OK|unix.X_OK) == nil
}

// TakeOverCgroup makes this process's cgroup one it may put children into, and
// reports where.
//
// Two halves, and neither works alone: a process cannot enable controllers for
// the cgroup it is *in*, so it steps aside into a leaf first, and only then may
// write the subtree mask its children need. Empty and no error where there is no
// cgroup to take over, because a machine without one is not a machine that has
// failed.
func TakeOverCgroup() (string, error) {
	base, ok := cgroupParent()
	if !ok {
		return "", nil
	}

	_, err := stepAside(base)
	if err != nil {
		return "", err
	}

	// And enable them for the children, which is the other half of taking a
	// cgroup over. Stepping aside makes the write *legal*; without the write
	// nothing is enabled, and the guest - which lives in the leaf - finds its
	// own cgroup offering no controllers to give a step.
	//
	// Measured: after the move the scope held no processes and
	// `cgroup.subtree_control` was still empty, so a step's ceiling was refused
	// with "the memory controller is not delegated to this cgroup" (E124).
	// The scope, returned so the host can tell the guest to put step cgroups
	// here rather than under the leaf the host now occupies.
	return base, enableControllers(base)
}

// enableControllers offers a cgroup's controllers to its children.
//
// Best effort per controller, in one write, which is what the kernel accepts.
// A machine that delegates only some of them should get those rather than none:
// a memory ceiling and no cpu weight is most of what a build wants.
func enableControllers(dir string) error {
	err := os.WriteFile(filepath.Join(dir, "cgroup.subtree_control"),
		[]byte("+memory +pids +cpu"), 0o644) // written by the kernel's rules
	if err == nil {
		return nil
	}

	// All-or-nothing failed, so ask for them one at a time and keep what is
	// granted. A single refused controller must not cost the others.
	var last error

	for _, c := range []string{"+memory", "+pids", "+cpu"} {
		werr := os.WriteFile(filepath.Join(dir, "cgroup.subtree_control"),
			[]byte(c), 0o644) // as above
		if werr != nil {
			last = werr
		}
	}

	if last != nil {
		return fmt.Errorf("enable controllers for the children of %s: %w", dir, last)
	}

	return nil
}

// stepAside moves this process into a leaf of base, so base can enable
// controllers for its other children.
//
// cgroup v2's **"no internal processes" rule**: a cgroup holding processes may
// not enable controllers in `cgroup.subtree_control`. A delegated scope holds
// the process that was started in it - the engine - so inside
// `systemd-run --user --scope --property=Delegate=yes` the controllers are
// listed as available and enabling them is still refused:
//
//	cgroup.controllers              cpu io memory pids
//	echo +memory > subtree_control  FAILS
//
// Which reads as "not delegated" unless you know the rule, and is why this is a
// named step rather than a retry.
//
// Idempotent: a leaf that already exists is reused, so a second call after a
// reconnect does not pile up directories.
// TakeOverCgroup steps this process's cgroup aside so its children's limits can
// be enforced. Exported because the caller has to be the **host**.
//
// The guest runs in a pid namespace, and pids written to `cgroup.procs` are
// interpreted in the writer's pid namespace - so a guest reading the host pids
// in its scope and writing them back is naming processes that do not exist for
// it. The move silently does nothing, `subtree_control` is refused exactly as
// before, and the fix is indistinguishable from no fix (E124).
//
// Idempotent, and a no-op where there is nothing to take over.
func stepAside(base string) (string, error) {
	leaf := filepath.Join(base, "earthbuild.main")

	err := os.MkdirAll(leaf, 0o755) //nolint:gosec // a cgroup directory's conventional mode
	if err != nil {
		return "", fmt.Errorf("create the engine's own cgroup: %w", err)
	}

	// **Everything in it, not just this process.** The engine is two processes
	// - the host and the guest it started - and both are in the delegated
	// scope. Moving only the guest leaves the host behind, the scope still
	// holds a process, and `subtree_control` is refused exactly as before: a
	// fix that looks right, changes the failure not at all, and is only
	// distinguishable by trying it (E124).
	//
	// Written one at a time because the kernel takes one pid per write, and
	// failures are ignored: a process that exited between the read and the
	// write is not an error, and one that cannot be moved leaves the scope
	// non-empty, which the caller finds out from the controller check.
	body, err := os.ReadFile(filepath.Join(base, "cgroup.procs")) //nolint:gosec // a cgroup path
	if err != nil {
		return "", fmt.Errorf("read the processes in %s: %w", base, err)
	}

	procs := filepath.Join(leaf, "cgroup.procs")

	f, err := os.OpenFile(procs, os.O_WRONLY, 0) //nolint:gosec // a cgroup path
	if err != nil {
		return "", fmt.Errorf("open %s: %w", procs, err)
	}

	defer f.Close()

	for pid := range strings.SplitSeq(strings.TrimSpace(string(body)), "\n") {
		if pid == "" {
			continue
		}

		_, _ = f.WriteString(pid)
	}

	return leaf, nil
}

// stepAsideSelf moves only this process into a leaf of base.
//
// The guest's version of the same rule, one level down. The host takes over the
// delegated scope and lands in a leaf; the guest is started inside that leaf and
// so *it* now holds a process, and cannot enable controllers for the step
// cgroups it is about to make.
//
// `"0"` rather than a pid, which is what makes this the guest's to do and the
// host's take-over not: the kernel reads a pid written here in the writer's pid
// namespace, and "0" means the writer whatever namespace it is in.
func stepAsideSelf(base string) error {
	leaf := filepath.Join(base, "earthbuild.guest")

	err := os.MkdirAll(leaf, 0o755) //nolint:gosec // a cgroup directory's conventional mode
	if err != nil {
		return fmt.Errorf("create the guest's own cgroup: %w", err)
	}

	err = os.WriteFile(filepath.Join(leaf, "cgroup.procs"), []byte("0"), 0o644) //nolint:gosec // as above
	if err != nil {
		return fmt.Errorf("move the guest into %s: %w", leaf, err)
	}

	return nil
}
