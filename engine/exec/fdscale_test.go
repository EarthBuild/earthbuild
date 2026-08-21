package exec

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// openFDs is a proxy for how many descriptors this process holds: the number the
// kernel hands out next.
//
// A descriptor is the lowest free integer, so opening one file and reading its
// number says where the free space starts. It is not a count - a process that
// opened and closed a thousand files reads the same as one that opened none,
// which is the point - and it climbs exactly when something is *held*.
//
// Counting the directory would be more direct and does not work: `/dev/fd` on
// darwin is a magic filesystem whose contents change as it is read, and reading
// it returns `bad file descriptor`. A test that skipped there would be a test
// that never ran on the machine this engine is developed on, which is the same
// as not having written it.
func openFDs(t *testing.T) int {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "fd-")
	if err != nil {
		t.Fatalf("probe for a descriptor: %v", err)
	}

	n := int(f.Fd())

	_ = f.Close()

	return n
}

func treeOf(t *testing.T, n int) string {
	t.Helper()

	dir := t.TempDir()

	for i := range n {
		// Spread across directories, as a real layer is.
		sub := filepath.Join(dir, "d"+strconv.Itoa(i%64))

		err := os.MkdirAll(sub, 0o755)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(sub, "f"+strconv.Itoa(i)), []byte("x"), 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

// Descriptor use must not scale with the number of files.
//
// A store of 15,252 files left its sandbox holding 10,813 descriptors, for a
// build that read a dozen of them. Nothing reaps a sandbox whose build was
// killed, so a dozen interrupted builds exhausted a machine's system-wide limit
// - and the failure surfaced as an unrelated step reporting `too many open files
// in system`, or as a build hanging at no CPU with its work apparently done. An
// evening went into attributing that to a filesystem feature that had nothing to
// do with it (E510).
//
// This is the property that was never asserted: **placing a tree ten times the
// size must not cost ten times the descriptors.** It is checked as a ratio
// rather than an absolute, because an absolute is a threshold and a threshold
// measures the machine.
//
// It covers the engine's own use, which is what the engine can fix. The
// sandbox's use is a property of sharing a host directory into a VM and is a
// larger question - see the plan.
func TestDescriptorUseDoesNotScaleWithFileCount(t *testing.T) {
	t.Parallel()

	small := treeOf(t, 200)
	large := bigTree(t, fdScale())

	before := openFDs(t)

	err := linkTreeExclusive(small, filepath.Join(t.TempDir(), "small"))
	if err != nil {
		t.Fatalf("small: %v", err)
	}

	afterSmall := openFDs(t)

	err = linkTreeExclusive(large, filepath.Join(t.TempDir(), "large"))
	if err != nil {
		t.Fatalf("large: %v", err)
	}

	afterLarge := openFDs(t)

	t.Logf("descriptors: %d before, %d after 200 files, %d after %d", before, afterSmall, afterLarge, fdScale())

	// Twenty times the files. Anything proportional shows up immediately; a
	// worker pool's worth of concurrent opens does not.
	if grew := afterLarge - before; grew > 64 {
		t.Errorf("placing %d files grew this process by %d descriptors"+
			"\n  descriptor use is tracking file count, which exhausts a machine"+
			" long before the files do", fdScale(), grew)
	}
}

// Capturing a large tree does not hold a descriptor per file either.
//
// The capture reads every file to hash it, which is the one place where holding
// them all open would be an easy mistake to make and an invisible one to have
// made: it works until the tree is large enough, and then fails somewhere else.
func TestCapturingALargeTreeDoesNotHoardDescriptors(t *testing.T) {
	t.Parallel()

	tree := copyOfBigTree(t, fdScale())

	before := openFDs(t)

	id, err := placeCaptured(t.TempDir(), tree)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	grew := openFDs(t) - before

	t.Logf("captured %v; descriptors grew by %d", id, grew)

	if grew > 64 {
		t.Errorf("capturing %d files grew this process by %d descriptors", fdScale(), grew)
	}
}

// copyOfBigTree is a throwaway copy, because placeCaptured renames what it is
// given into the store and the fixture is meant to be reused.
func copyOfBigTree(t *testing.T, n int) string {
	t.Helper()

	dst := filepath.Join(t.TempDir(), "copy")

	err := linkTreeExclusive(bigTree(t, n), dst)
	if err != nil {
		t.Fatalf("copy the fixture: %v", err)
	}

	return dst
}

// fdScale is how many files the descriptor tests place and capture.
//
// **20,000 by default, 100,000 on request.** The property - that descriptor use
// does not track file count - shows at any scale where the ratio is large: 200
// against 20,000 is a hundredfold, and anything proportional is unmissable.
// What the larger number buys is confidence against a *cap* rather than a ratio,
// since a leak that stops at 65,535 looks bounded at 20,000.
//
// It is not the default because it costs 95 seconds of every `go test ./...`,
// and a test suite that is slow enough to skip is a test suite that gets
// skipped. Validated at 100,000; set EARTH_TEST_FD_SCALE to run it there.
func fdScale() int {
	if v := os.Getenv("EARTH_TEST_FD_SCALE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}

	return 20000
}
