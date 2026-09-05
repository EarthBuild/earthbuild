//go:build linux

package guest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

func requireCgroups(t *testing.T) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("cgroups need root")
	}

	err := os.MkdirAll("/sys/fs/cgroup/earthbuild/probe", 0o750)
	if err != nil {
		t.Skip("cgroup v2 not delegated to this container")
	}

	os.Remove("/sys/fs/cgroup/earthbuild/probe")
}

// TestMemoryLimitKillsARunawayStep: a step that allocates past its ceiling must
// die, not take the guest with it.
//
// This is why CLONE_INTO_CGROUP matters rather than adding the pid afterwards:
// a process enrolled after fork runs unconstrained between exec and enrolment,
// so a step that allocates immediately can exceed the ceiling before it exists.
func TestMemoryLimitKillsARunawayStep(t *testing.T) {
	t.Parallel()

	requireCgroups(t)

	root := t.TempDir()

	self, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		t.Skip("cannot read own binary")
	}

	err = os.WriteFile(filepath.Join(root, "probe"), self, 0o755) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}

	srv := &guest.Server{
		Mat:    &fixedRootMat{root: root},
		Limits: guest.Limits{MemoryMax: 16 << 20, PidsMax: 64},
	}
	c := pairWith(t, srv)

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	code, _, err := c.Exec(context.Background(), h,
		[]string{"/probe", "-test.run", "TestProbeAllocates"},
		[]string{"EARTH_PROBE=1", "EARTH_PROBE_ALLOC=268435456"})
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	if reason := srv.Degraded(); reason != "" {
		t.Skipf("limits not applied here: %s", reason)
	}

	if code == 0 {
		t.Error("a step allocating 256 MiB under a 16 MiB ceiling was not stopped")
	}
}

// TestProbeAllocates runs inside the cgroup and tries to exceed it.
func TestProbeAllocates(t *testing.T) {
	t.Parallel()

	if os.Getenv("EARTH_PROBE") == "" {
		t.Skip("not the probe")
	}

	n := 256 << 20

	// Touch every page, so the allocation is resident rather than reserved.
	buf := make([]byte, n)
	for i := 0; i < len(buf); i += 4096 {
		buf[i] = 1
	}

	t.Logf("allocated %d bytes without being stopped", len(buf))
}

// TestCgroupsAreRemoved: a cgroup that outlives its step leaks, and thousands
// of them degrade a machine in ways nobody traces back to the build tool.
func TestCgroupsAreRemoved(t *testing.T) {
	t.Parallel()

	requireCgroups(t)

	root := t.TempDir()

	c := pairWith(t, &guest.Server{
		Mat:        &fixedRootMat{root: root},
		Limits:     guest.Limits{MemoryMax: 64 << 20},
		Unconfined: true, // no chroot, so /bin/true is reachable
	})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	_, _, err = c.Exec(context.Background(), h, []string{"/bin/true"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir("/sys/fs/cgroup/earthbuild")
	if err != nil {
		return // never created; nothing leaked
	}

	// A cgroup directory holds its own interface files, so only sub-directories
	// are child cgroups. Counting every entry reports eighty-three leaks on a
	// clean run, which is how this test failed before it was right.
	var leaked []string

	for _, e := range entries {
		if e.IsDir() {
			leaked = append(leaked, e.Name())
		}
	}

	if len(leaked) != 0 {
		t.Errorf("%d cgroups outlived their steps: %v", len(leaked), leaked)
	}
}
