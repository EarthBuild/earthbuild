package guestd

import (
	"errors"
	"os"
	"testing"
)

// A scratch directory made for a mount that failed does not survive it.
//
// `procForTracing` made one with MkdirTemp and returned without it on three of
// four paths, so a guest that could not mount a procfs left a directory behind
// every time it started. 1625 of them were found in `/tmp` on the build box,
// beside 2890 from the other leak, on a root filesystem with 47 MB free (E473).
func TestAScratchDirectoryDoesNotOutliveAFailedMount(t *testing.T) {
	t.Parallel()

	// The directory is learned from the mount that was handed it, not by
	// globbing the temp directory: a glob sees every previous run's litter as
	// well as this run's, and this test failed on leftovers from its own
	// mutant. *An observable wider than the thing being observed reports other
	// people's news* (E473).
	var made string

	dir, err := mountScratch("eb-scratch-test", func(d string) error {
		made = d

		return errors.New("no")
	})
	if err == nil {
		t.Fatal("a mount that failed was reported as working")
	}

	if dir != "" {
		t.Errorf("a directory was returned for a mount that failed: %s", dir)
	}

	if made == "" {
		t.Fatal("no directory was made, so the mount was never given one")
	}

	_, err = os.Stat(made)
	if !os.IsNotExist(err) {
		t.Errorf("%s outlived the mount it was made for (%v)"+
			"\n  a temporary that outlives its owner is not temporary", made, err)
	}
}

// A mount that worked keeps its directory, which is the whole point of it.
func TestAScratchDirectorySurvivesAMountThatWorked(t *testing.T) {
	t.Parallel()

	var mounted string

	dir, err := mountScratch("eb-scratch-kept", func(d string) error {
		mounted = d

		return nil
	})
	if err != nil {
		t.Fatalf("a mount that worked was reported as failing: %v", err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	if dir != mounted {
		t.Errorf("mounted %q and returned %q, so the caller uses a directory"+
			" nothing was mounted on", mounted, dir)
	}

	_, err = os.Stat(dir)
	if err != nil {
		t.Errorf("the directory the mount is on is gone: %v", err)
	}
}
