//go:build unix

package main

import "testing"

// A second sweep in the same worktree is refused rather than run.
//
// Two sweeps mutate each other's files: one applies a mutant while the other
// reads the same source, and the reader reports "0 matches in x.go, want
// exactly 1". That verdict names the catalogue, so the reader goes and fixes an
// entry which was always correct - and the sweep that caused it has since put
// the file back, leaving nothing to find. An hour went into that before this
// existed, and it had already cost two verdicts once before.
//
//nolint:paralleltest // takes a lock keyed on a directory
func TestASecondSweepInOneWorktreeIsRefused(t *testing.T) {
	dir := t.TempDir()

	release, err := lockSweep(dir)
	if err != nil {
		t.Fatalf("the first sweep must be able to start: %v", err)
	}

	_, err = lockSweep(dir)
	if err == nil {
		t.Error("a second sweep started against a worktree a sweep already holds" +
			"\n  the two will mutate each other's files and blame the catalogue for it")
	}

	release()

	// Released, not merely dropped: a sweep that finished must not lock the
	// worktree out of the next one.
	release, err = lockSweep(dir)
	if err != nil {
		t.Errorf("a finished sweep left the worktree locked: %v", err)
	} else {
		release()
	}
}
