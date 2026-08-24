package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// bigTree returns a tree of n files, generated once and reused thereafter.
//
// **Not committed.** A hundred thousand files is a repository nobody can clone
// and a diff nobody can read, for content that is a `for` loop. It is generated
// on demand into gitignored `testdata/`, and the second run of any test that
// wants it finds it already there.
//
// **Stamped with a fixed time, and that is not tidiness.** A layer's identity
// includes mtimes (§3.3a, I8), so a fixture stamped by the clock is a different
// layer every time it is regenerated - and a test asserting anything about its
// digest would pass on the machine that built it and fail on the next one, in a
// way that looks like the engine being non-deterministic rather than the
// fixture.
//
// **Completion is marked last, and beside the tree.** A generation interrupted
// half way leaves a directory that looks finished and is not, which is a fixture
// that silently tests a smaller tree than it says. The marker names the count,
// so asking for a different size regenerates rather than reusing whatever is
// there - and it sits outside the directory, because writing into a directory
// changes its mtime and mtimes are part of what a layer is.
var bigTreeLocks sync.Map

func bigTree(t *testing.T, n int) string {
	t.Helper()

	// One builder per size in this process. The rename below makes a concurrent
	// build correct; this makes it cheap.
	held, _ := bigTreeLocks.LoadOrStore(n, &sync.Mutex{})
	lock, _ := held.(*sync.Mutex)

	lock.Lock()
	defer lock.Unlock()

	dir := filepath.Join("testdata", fmt.Sprintf("bigtree-%d", n))

	// **Beside the tree, not inside it.** Writing the marker into the directory
	// updates that directory's mtime, and a layer's identity includes mtimes -
	// so a marker written after stamping un-stamps the thing it marks. The
	// layer store keeps `.own` and `.config.json` beside a layer for the same
	// reason: anything inside would be a file the layer does not have, and the
	// digest would name it.
	marker := dir + ".complete"

	// Both, because either alone lies: a marker outlives a directory somebody
	// deleted, and a directory without a marker is one that may be half built.
	if b, err := os.ReadFile(marker); err == nil && string(b) == fmt.Sprint(n) {
		_, err := os.Stat(dir)
		if err == nil {
			return dir
		}
	}

	// Whatever is there is the wrong size or unfinished; neither is worth
	// keeping.
	err := os.RemoveAll(dir)
	if err != nil {
		t.Fatalf("clear a stale fixture: %v", err)
	}

	_ = os.Remove(marker)

	// Built under a name of its own and renamed into place. Two parallel tests
	// asking for the same fixture otherwise race: one reads the directory while
	// the other is still filling it, and reads a smaller tree than it asked for
	// - silently, because a fixture has no way to say it is half built. The
	// same stage-then-rename the layer store uses, for the same reason.
	staging := fmt.Sprintf("%s.building-%d", dir, os.Getpid())

	err = os.RemoveAll(staging)
	if err != nil {
		t.Fatalf("clear a stale fixture: %v", err)
	}

	defer func() { _ = os.RemoveAll(staging) }()

	stamp := time.Unix(1000000000, 0)

	err = os.MkdirAll(staging, 0o750)
	if err != nil {
		t.Fatalf("build the fixture: %v", err)
	}

	// Spotlight indexes anything under a checkout, and a hundred thousand files
	// is a hundred thousand files to index: `mds_stores` sat at 104% CPU after
	// this fixture first appeared, which is a benchmark ruined by a test that is
	// not even running. macOS honours this marker; elsewhere it is a file nobody
	// reads.
	//
	// **Written before the stamping, not after.** It has to live inside the
	// tree to work, so it is part of what the tree digests to - and a file added
	// after the mtimes were fixed carries the wall clock and re-dirties the
	// directory it lands in. The same mistake as the completion marker, which
	// could be moved outside; this one cannot.
	indexed := filepath.Join(staging, ".metadata_never_index")

	err = os.WriteFile(indexed, nil, 0o600)
	if err != nil {
		t.Fatalf("mark the fixture unindexable: %v", err)
	}

	err = os.Chtimes(indexed, stamp, stamp)
	if err != nil {
		t.Fatalf("stamp the fixture: %v", err)
	}

	// Spread across directories: a layer is not one flat directory, and a
	// filesystem behaves differently when it is.
	for i := range n {
		sub := filepath.Join(staging, fmt.Sprintf("d%02d", i%64), fmt.Sprintf("e%02d", (i/64)%64))

		err := os.MkdirAll(sub, 0o750)
		if err != nil {
			t.Fatalf("build the fixture: %v", err)
		}

		at := filepath.Join(sub, fmt.Sprintf("f%d", i))

		err = os.WriteFile(at, fmt.Appendf(nil, "file %d\n", i), 0o600)
		if err != nil {
			t.Fatalf("build the fixture: %v", err)
		}

		err = os.Chtimes(at, stamp, stamp)
		if err != nil {
			t.Fatalf("stamp the fixture: %v", err)
		}
	}

	// Directories last and deepest-first, because writing into a directory
	// changes its mtime.
	var dirs []string

	err = filepath.Walk(staging, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.IsDir() {
			dirs = append(dirs, p)
		}

		return err
	})
	if err != nil {
		t.Fatalf("stamp the fixture: %v", err)
	}

	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Chtimes(dirs[i], stamp, stamp)
	}

	err = os.Rename(staging, dir)
	if err != nil {
		// Somebody else finished first, which is a race worth losing: their
		// tree was built by this function from the same loop.
		_, again := os.Stat(dir)
		if again != nil {
			t.Fatalf("place the fixture: %v", err)
		}
	}

	err = os.WriteFile(marker, []byte(fmt.Sprint(n)), 0o600)
	if err != nil {
		t.Fatalf("mark the fixture complete: %v", err)
	}

	return dir
}

// The same fixture, twice, is the same layer.
//
// The property the fixed stamp exists for: regenerating it must not change what
// it is. A fixture that digests differently after a `git clean` turns every
// digest assertion into a machine-dependent one.
func TestARegeneratedFixtureIsTheSameLayer(t *testing.T) {
	t.Parallel()

	// Captured in place: placing it first would measure the placing.
	first, err := layer.TakeOwnedIn(bigTree(t, 500), layer.IDMap{}, layer.IDMap{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Force a regeneration, exactly as a clean checkout would.
	err = os.RemoveAll(filepath.Join("testdata", "bigtree-500"))
	if err != nil {
		t.Fatal(err)
	}

	second, err := layer.TakeOwnedIn(bigTree(t, 500), layer.IDMap{}, layer.IDMap{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if first.ID != second.ID {
		t.Errorf("the fixture regenerated as a different layer:\n  %v\n  %v"+
			"\n  a digest assertion over it would depend on when it was built", first.ID, second.ID)
	}
}
