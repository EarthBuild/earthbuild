package interp_test

import (
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A shared cache digests one path once, however many builds ask.
//
// Counted rather than timed, because what is claimed is "the files are read
// once" and a clock would answer a different question badly - the same trade
// E350 records, and the one the corpus harness records failing when the tree is
// small enough that the reads are free.
//
// `DigestedForTest` counts files whose contents this process has read, which is
// exactly the work the cache exists to remove.
func TestASharedContextCacheDigestsOncePerPath(t *testing.T) {
	// Not parallel: the counter it reads is the package's, and another test
	// digesting a tree at the same moment would be counted here.
	dir := contextHolding(t, "data/f", "hello")

	const src = `
main:
    FROM alpine:3.22
    COPY data/f /f
`

	shared := &interp.ContextCache{}

	before := layer.DigestedForTest()

	for range 3 {
		_, err := interp.Build(versioned+src, testMain,
			interp.WithContext(dir), interp.WithContextCache(shared))
		if err != nil {
			t.Fatal(err)
		}
	}

	withCache := layer.DigestedForTest() - before

	before = layer.DigestedForTest()

	for range 3 {
		_, err := interp.Build(versioned+src, testMain, interp.WithContext(dir))
		if err != nil {
			t.Fatal(err)
		}
	}

	without := layer.DigestedForTest() - before

	if withCache >= without {
		t.Errorf("three builds sharing a cache read %d files and three without"+
			" read %d; the cache is not being consulted", withCache, without)
	}

	// One pass over the tree, not three. Stated as a bound rather than an
	// equality because a plan may name more than one path.
	if withCache > without/2 {
		t.Errorf("sharing saved only %d reads of %d, which is not once per path",
			without-withCache, without)
	}
}

// The cache is used from several goroutines at once, as its doc claims.
//
// Planning several targets concurrently is the obvious reason to share one, so
// the claim is not idle - and a map read while another goroutine writes it is
// the kind of fault that appears once a month on somebody else's machine. Run
// under -race this either passes or names the two goroutines.
func TestAContextCacheIsSafeToShareBetweenPlans(t *testing.T) {
	t.Parallel()

	dir := contextHolding(t, "data/f", "hello")
	shared := &interp.ContextCache{}

	const src = `
main:
    FROM alpine:3.22
    COPY data/f /f
`

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			_, err := interp.Build(versioned+src, testMain,
				interp.WithContext(dir), interp.WithContextCache(shared))
			if err != nil {
				t.Errorf("planning: %v", err)
			}
		})
	}

	wg.Wait()
}
