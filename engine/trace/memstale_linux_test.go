//go:build linux

package trace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAStaleMemoryFileIsReopenedRatherThanBelieved.
//
// **A descriptor on `/proc/<pid>/mem` is bound to the memory map it was opened
// against, not to the process.** A traced step forks a shell which execs, and
// exec replaces that map - so a descriptor cached across it reads `EIO` for a
// process that is alive, well, and stopped in the syscall this engine is
// supposed to be answering.
//
// Keeping one open is worth 4.25µs of a 6.9µs handler (E681), and it was kept
// without this: the read failed, the observation was declared incomplete, and
// a step that should have had a file faulted in took the absent branch instead.
// `TestTheTracerIsHandedTheFiller` is what noticed, in CI and not here, because
// where `cat` lives decides how many times the shell execs before it finds one.
//
// Failing safe is not the same as working. The answer is to stop believing the
// descriptor: drop it and open the process again, which is what an uncached
// reader did every time.
func TestAStaleMemoryFileIsReopenedRatherThanBelieved(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// A file that reads as a NUL-terminated path, standing in for the target's
	// memory; and one that is closed, standing in for a map that has gone.
	good := filepath.Join(dir, "good")

	err := os.WriteFile(good, append([]byte("/etc/hostname"), 0), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	stale, err := os.Open(good)
	if err != nil {
		t.Fatal(err)
	}

	_ = stale.Close() // every read through this now fails, as after an exec

	opens := 0

	m := &memFiles{open: func(uint32) (*os.File, error) {
		opens++

		if opens == 1 {
			return stale, nil
		}

		return os.Open(good) //nolint:gosec // a path this test made
	}}

	defer func() { _ = m.Close() }()

	// The first attempt caches the stale descriptor and its read fails; the
	// second must not be answered from it.
	got, err := pathVia(m, 1234, 0)
	if err != nil {
		t.Fatalf("a stale descriptor was believed rather than reopened: %v", err)
	}

	if got != "/etc/hostname" {
		t.Errorf("read %q, want the path behind the reopened descriptor", got)
	}

	if opens != 2 {
		t.Errorf("the target was opened %d times, want 2 - once for the stale"+
			"\n  descriptor and once to replace it", opens)
	}
}
