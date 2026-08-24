//go:build linux

package trace_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/trace"
)

// How much of what a step stands on does it actually read?
//
// **The number lazy transfer lives or dies on.** Sending only the paths a step
// will read is worth doing if a step reads a small fraction of its base, and is
// a round trip per file if it reads most of it. Container runtimes assume the
// first; this engine can measure it, because it observes read sets already
// (E283).
//
// Reported rather than asserted. The figure depends on the command and on the
// machine's filesystem, and a test that failed on somebody's laptop for having a
// different `/usr` would be a test nobody reads - which is the same reasoning as
// the corpus build test's.
func TestWhatFractionOfATreeAStepReads(t *testing.T) {
	if os.Getenv("EARTH_TEST_TRACE_FRACTION") == "" {
		t.Skip("set EARTH_TEST_TRACE_FRACTION=1 to measure how much of a tree a" +
			" step reads")
	}

	// A tree that stands in for a base. `/usr` on most machines; on NixOS
	// everything real is elsewhere, and the point of the measurement is what a
	// step reads out of a *populated* tree.
	root := os.Getenv("EARTH_TEST_TRACE_ROOT")
	if root == "" {
		root = "/usr"
	}

	// Through the symlink: `/run/current-system/sw` is one, and walking a
	// symlink finds one file.
	if actual, err := filepath.EvalSymlinks(root); err == nil {
		root = actual
	}

	total := 0

	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner is not the measurement
		}

		if !d.IsDir() {
			total++
		}

		return nil
	})
	if err != nil {
		t.Skipf("cannot walk %s: %v", root, err)
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"a shell that does nothing", []string{"/bin/sh", "-c", "true"}},
		{"a shell that lists a directory", []string{"/bin/sh", "-c", "ls /usr/bin > /dev/null"}},
		{"a compiler printing its version", []string{"/bin/sh", "-c", "cc --version > /dev/null 2>&1 || true"}},
	} {
		seen := traced(t, tc.args)

		read := 0

		for p := range seen {
			if strings.HasPrefix(p, root+"/") {
				read++
			}
		}

		// The headline is the ratio of what a step *named* to what its tree
		// holds. The prefix count is kept because on a machine whose binaries
		// live under the tree it is the same number, and on one whose do not -
		// NixOS resolves through the store rather than the profile - the
		// difference is worth seeing rather than hiding.
		pct := 100 * float64(len(seen)) / float64(max(total, 1))

		t.Logf("%-34s named %4d paths against %6d files in the tree (%.3f%%)"+
			" [%d under %s]",
			tc.name, len(seen), total, pct, read, root)
	}
}

// traced runs a command under the tracer and returns the paths it named.
//
// **On its own thread, every time.** A filter cannot be taken off a thread, and
// the thread is deliberately never unlocked (E206) - so a second measurement on
// the same goroutine installs a second filter on an already-filtered thread and
// sees nothing. The first version of this reported 54 paths for one command and
// zero for the two after it, which reads exactly like a step that touched
// nothing.
func traced(t *testing.T, args []string) map[string]bool {
	t.Helper()

	type result struct {
		paths map[string]bool
		err   error
	}

	got := make(chan result, 1)

	go func() {
		runtime.LockOSThread() // and never unlocked: the thread ends with this goroutine

		tr, err := trace.StartOnSelf()
		if err != nil {
			got <- result{err: err}

			return
		}

		done := make(chan struct{})

		go func() { tr.Run(); close(done) }()

		cmd := exec.CommandContext(t.Context(), args[0], args[1:]...)
		_ = cmd.Run()

		_ = tr.Close()
		<-done

		out := map[string]bool{}
		for _, p := range tr.Sightings().Paths {
			out[p] = true
		}

		got <- result{paths: out}
	}()

	r := <-got
	if r.err != nil {
		t.Skipf("no seccomp user notification here: %v", r.err)
	}

	return r.paths
}
