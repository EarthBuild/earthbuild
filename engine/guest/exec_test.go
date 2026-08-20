package guest_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/sim"
)

// TestExecReturnsTheExitCode: a non-zero exit is a *result*, not a protocol
// error. The step ran and failed, which the engine records and caches like any
// other outcome - conflating the two would make a failing build
// indistinguishable from a broken guest.
func TestExecReturnsTheExitCode(t *testing.T) {
	t.Parallel()

	if !guest.NeedsIsolation(t) {
		return
	}

	// A real root, because the simulator holds no filesystem and its root
	// deliberately names nothing - the fidelity contract refusing to pretend.
	c := pair(t, &fixedRootMat{root: stepRoot(t)})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	for _, tc := range []struct {
		name string
		argv []string
		want int
	}{
		{"success", []string{testTrue}, 0},
		{"failure", []string{"false"}, 1},
		{"specific code", []string{"sh", "-c", "exit 42"}, 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			code, _, err := c.Exec(context.Background(), h, tc.argv, nil)
			if err != nil {
				t.Fatalf("a failing step was reported as a protocol error: %v", err)
			}

			if code != tc.want {
				t.Errorf("exit = %d, want %d", code, tc.want)
			}
		})
	}
}

// TestUnstartableCommandsAreProtocolErrors is the other half of that
// distinction: a step that could not be started at all did not run, so it has
// no exit code to report and must not be recorded as having failed.
func TestUnstartableCommandsAreProtocolErrors(t *testing.T) {
	t.Parallel()

	c := pair(t, &fixedRootMat{root: stepRoot(t)})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	_, _, err = c.Exec(context.Background(), h, []string{"definitely-not-a-command"}, nil)
	if err == nil {
		t.Error("an unstartable command was reported as a successful run")
	}

	_, _, err = c.Exec(context.Background(), h, nil, nil)
	if err == nil {
		t.Error("an empty argv was accepted")
	}
}

// TestOnlyDeclaredEnvironmentReachesTheStep guards invariant I3 by omission.
//
// If the guest's own environment leaked in, a step could observe ambient state
// that never entered its cache key - and two builds differing only in that
// state would share a cache entry.
func TestOnlyDeclaredEnvironmentReachesTheStep(t *testing.T) {
	if !guest.NeedsIsolation(t) {
		return
	}

	t.Setenv("EARTHBUILD_LEAK_CANARY", "leaked")

	c := pair(t, &fixedRootMat{root: stepRoot(t)})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	_, out, err := c.Exec(context.Background(), h,
		[]string{"sh", "-c", "echo [$EARTHBUILD_LEAK_CANARY][$DECLARED]"},
		[]string{"DECLARED=yes"})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out, "leaked") {
		t.Errorf("the guest's environment reached the step: %q", out)
	}

	if !strings.Contains(out, "yes") {
		t.Errorf("the declared environment did not reach the step: %q", out)
	}
}

// TestExecRunsInTheMaterialisedRoot: a step must act on the filesystem it was
// given, not on whatever the guest's working directory happened to be.
func TestExecRunsInTheMaterialisedRoot(t *testing.T) {
	t.Parallel()

	if !guest.NeedsIsolation(t) {
		return
	}

	root := t.TempDir()

	c := pair(t, &fixedRootMat{root: root})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	if _, _, err := c.Exec(context.Background(), h,
		[]string{"sh", "-c", "echo written > out.txt"}, nil); err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(root, "out.txt"))
	if err != nil {
		t.Errorf("the step did not write into the materialised root: %v", err)
	}
}

// TestExecRejectsUnknownHandles: a handle is a capability, and one that was
// never issued must not be usable.
func TestExecRejectsUnknownHandles(t *testing.T) {
	t.Parallel()

	c := pair(t, &sim.Materialiser{})

	h, err := c.Materialise(context.Background(), []ir.NodeID{{1}})
	if err != nil {
		t.Fatal(err)
	}

	err = h.Release()
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = c.Exec(context.Background(), h, []string{testTrue}, nil)
	if err == nil {
		t.Error("a released handle was still usable for exec")
	}
}
