//go:build linux

package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writable makes a directory and returns it.
func writable(t *testing.T, parts ...string) string {
	t.Helper()

	p := filepath.Join(parts...)

	err := os.MkdirAll(p, 0o755) //nolint:gosec // a cgroup directory's conventional mode
	if err != nil {
		t.Fatal(err)
	}

	return p
}

// The step cgroup goes where this process may actually create one.
//
// `cgroupRoot` was the constant `/sys/fs/cgroup` and the parent was
// `<root>/earthbuild`, so an unprivileged build could never make one: that
// directory belongs to root, and every step ran unbounded (E123).
//
// The measurement that decides the design: cgroup v2 delegates a writable
// subtree to a user session, and a process may be *placed* only in a cgroup
// under a common ancestor it can write - so a process started inside a
// delegated scope can create children of its own cgroup and move steps into
// them, while one started outside cannot be moved in at all.
//
//	rootless, plain session       /sys/fs/cgroup/earthbuild   permission denied
//	rootless, systemd-run --scope <own cgroup>/earthbuild     works
//	root                          /sys/fs/cgroup/earthbuild   works
//
// So the parent is found rather than assumed: this process's own cgroup when it
// can write there, and the root otherwise. Nothing is created by asking.
func TestTheStepCgroupGoesWhereThisProcessMayWrite(t *testing.T) {
	t.Parallel()

	t.Run("its own cgroup, when that is writable", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		own := writable(t, root, "user.slice", "user-1000.slice", "session.scope")

		got, ok := cgroupParentIn(root, "0::/user.slice/user-1000.slice/session.scope")
		if !ok {
			t.Fatal("no writable cgroup found where one exists")
		}

		if got != own {
			t.Errorf("chose %s, want this process's own cgroup %s", got, own)
		}
	})

	t.Run("the root, when that is what is writable", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()

		// The process's own cgroup is not there at all, which is what a
		// container with a masked /sys/fs/cgroup looks like.
		got, ok := cgroupParentIn(root, "0::/absent.scope")
		if !ok {
			t.Fatal("the root is writable and was not chosen")
		}

		if got != root {
			t.Errorf("chose %s, want the root %s", got, root)
		}
	})

	t.Run("nothing, when nothing is writable", func(t *testing.T) {
		t.Parallel()

		root := writable(t, t.TempDir(), "cg")

		err := os.Chmod(root, 0o500)
		if err != nil {
			t.Skipf("cannot make a read-only directory here: %v", err)
		}

		if os.Geteuid() == 0 {
			t.Skip("running as root, which can write a read-only directory")
		}

		if _, ok := cgroupParentIn(root, "0::/absent.scope"); ok {
			t.Error("a directory this process cannot write was reported as usable," +
				" so the failure moves to the step and reads as a kernel refusal")
		}
	})

	// A malformed or missing /proc/self/cgroup is not a reason to fail: the
	// root is still a candidate, and a build on a machine whose procfs is
	// unusual should degrade to what it can do rather than to nothing.
	t.Run("an unreadable own-cgroup line falls back", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()

		got, ok := cgroupParentIn(root, "this is not a cgroup line")
		if !ok || got != root {
			t.Errorf("a malformed cgroup line gave %q, %v; want the root", got, ok)
		}
	})
}

// Enabling a controller needs the parent to hold no processes.
//
// cgroup v2's "no internal processes" rule: a cgroup with processes in it may
// not enable controllers in `cgroup.subtree_control`. A delegated scope holds
// the process that was started in it - which is the engine - so the engine has
// to step aside into a leaf before it can enable anything for its steps.
//
// Measured inside `systemd-run --user --scope --property=Delegate=yes`:
//
//	cgroup.controllers       cpu io memory pids
//	echo +memory > subtree_control   FAILS
//
// The controllers are delegated and the write is still refused, which reads as
// "not delegated" unless you know the rule. That is why this is a named step
// with its own test rather than a retry.
func TestTheEngineStepsAsideBeforeEnablingControllers(t *testing.T) {
	t.Parallel()

	base := t.TempDir()

	// The files a cgroup directory has, so the code under test is exercised
	// rather than skipping on a missing file.
	for _, f := range []string{"cgroup.controllers", "cgroup.subtree_control"} {
		err := os.WriteFile(filepath.Join(base, f), nil, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Two processes, because the engine is two - the host and the guest it
	// started - and both are in the delegated scope. Moving one and not the
	// other leaves the scope non-empty and the controllers still refused, which
	// is a fix indistinguishable from no fix.
	err := os.WriteFile(filepath.Join(base, "cgroup.procs"), []byte("111\n222\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// cgroupfs creates a directory's interface files with the directory. A temp
	// directory does not, so the fixture does - rather than the code opening
	// with O_CREATE, which would be inert on cgroupfs and would silently write
	// pids into an ordinary file anywhere else.
	err = os.MkdirAll(filepath.Join(base, "earthbuild.main"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(base, "earthbuild.main", "cgroup.procs"), nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	leaf, err := stepAside(base)
	if err != nil {
		t.Fatalf("could not step aside: %v", err)
	}

	if filepath.Dir(leaf) != base {
		t.Errorf("the leaf %s is not a child of %s", leaf, base)
	}

	// The pid was written there, which is the whole point: a parent with no
	// processes is what lets the controllers be enabled.
	b, err := os.ReadFile(filepath.Join(leaf, "cgroup.procs")) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("nothing was written to the leaf's cgroup.procs: %v", err)
	}

	for _, want := range []string{"111", "222"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("process %s was left in the parent, which still holds one"+
				" and so still refuses to enable controllers: leaf has %q", want, b)
		}
	}
}
