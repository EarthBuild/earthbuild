package cache_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func key(b byte) core.Key { return core.Key{b} }

func entry(b byte) core.Entry {
	return core.Entry{Layer: ir.NodeID{b}, Exit: 0, Bytes: 42, Writer: testKey}
}

// The point of the thing: an entry written by one build is found by the next.
func TestEntriesSurviveTheProcess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	first, err := cache.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	first.Put(key(1), entry(7))

	// A second Open is what the next `earth-native build` does.
	second, err := cache.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := second.Get(key(1))
	if !ok {
		t.Fatal("an entry written by one process was not found by the next")
	}

	if got.Layer != (ir.NodeID{7}) || got.Bytes != 42 || got.Writer != testKey {
		t.Errorf("entry came back as %+v", got)
	}
}

// A corrupt entry is a miss, not a crash and not a wrong answer.
//
// This is the property the whole cache design turns on: an action-cache entry is
// an unverifiable claim (green paper §5.2), so anything that cannot be read as
// one is discarded. A cache that has been damaged - a truncated write, a bad
// disk, someone editing files - costs time and nothing else.
func TestCorruptEntriesAreMissesNotFailures(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	c, err := cache.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	c.Put(key(2), entry(8))

	// Corrupt every stored entry.
	entries, err := os.ReadDir(filepath.Join(dir, "actions"))
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		p := filepath.Join(dir, "actions", e.Name())

		// Named, so it does not shadow the cache's own err above (govet shadow).
		writeErr := os.WriteFile(p, []byte("{not json at all"), 0o600)
		if writeErr != nil {
			t.Fatal(writeErr)
		}
	}

	reopened, err := cache.Open(dir)
	if err != nil {
		t.Fatalf("a damaged cache must still open: %v", err)
	}

	if _, ok := reopened.Get(key(2)); ok {
		t.Error("a corrupt entry was returned as a usable claim")
	}
}

// An entry naming no layer is not a claim about anything.
//
// The zero NodeID is a well-formed digest, so a truncated or hand-edited entry
// can easily name it - and a build trusting that would materialise an empty base
// and cache the result.
func TestEntriesWithoutALayerAreRejected(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	c, err := cache.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	c.Put(key(3), core.Entry{Writer: testKey}) // no Layer

	if _, ok := c.Get(key(3)); ok {
		t.Error("an entry naming no layer was stored and returned")
	}
}

// Two builds writing at once must not corrupt each other's entries.
func TestConcurrentWritersDoNotCorrupt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	c, err := cache.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	// From 1: NodeID{0} is the zero digest, which Put rejects by design, so a
	// fixture starting at zero tests the rejection rather than concurrency.
	for i := 1; i <= 32; i++ {
		wg.Go(func() {
			c.Put(key(byteOf(i)), entry(byteOf(i)))
		})
	}

	wg.Wait()

	reopened, err := cache.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 32; i++ {
		got, ok := reopened.Get(key(byte(i)))
		if !ok {
			t.Errorf("entry %d is missing", i)

			continue
		}

		if got.Layer != (ir.NodeID{byte(i)}) {
			t.Errorf("entry %d came back as %s", i, got.Layer)
		}
	}
}

// A cache directory that cannot be created is reported, not swallowed: a build
// silently running without a cache is a build nobody can explain the speed of.
func TestAnUnusableDirectoryIsReported(t *testing.T) {
	t.Parallel()

	f := filepath.Join(t.TempDir(), "a-file")
	err := os.WriteFile(f, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = cache.Open(f)
	if err == nil {
		t.Error("a cache rooted at a regular file was accepted")
	}
}
