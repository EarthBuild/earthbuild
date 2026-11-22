package cacheshare_test

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/cacheshare"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// One cache directory has one writer at a time, whatever the author declared.
//
// **The gate the `shared` mode does not cover.** `core.ClaimOrder` and
// `guest.LockOrder` serialise steps that declare `--sharing=locked`, so a
// stock-run-share sequence over one of those is already alone in the directory.
// `--sharing=shared` is the author saying several steps may use it at once and
// the tools inside cope - which is an assertion about *npm's* locking and
// *cargo's*, and an importer is not one of those tools.
//
// So this engine serialises its own writers rather than inferring permission
// from a claim that was never about them.
func TestOneCacheIsStockedByOneStepAtATime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "mounts", "k", "scope")
	m := ir.Mount{ID: "k", Portable: true, Helper: "./none.wasm"}

	s := cacheshare.New(root, "", nil)

	var busy inFlight

	s.Away(&blocking{at: &busy})
	s.Told(func(string) (string, bool) {
		return ir.DigestOf([]byte("a map nobody holds")).String(), true
	})

	var wg sync.WaitGroup

	for range 4 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_ = s.Stock(context.Background(), m, dir)
		}()
	}

	wg.Wait()

	if got := busy.most.Load(); got > 1 {
		t.Errorf("%d steps were in one cache directory at once"+
			"\n  an importer writes raw files, which is not what --sharing=shared"+
			" says the tools inside can cope with", got)
	}
}

// And two different caches do not wait for each other.
//
// The reason `mountLocks` is per id rather than one lock over all mounts: steps
// using unrelated caches waiting on each other is a real cost paid for nothing.
func TestTwoCachesAreStockedAtOnce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := cacheshare.New(root, "", nil)

	var busy inFlight

	s.Away(&blocking{at: &busy})
	s.Told(func(string) (string, bool) {
		return ir.DigestOf([]byte("a map nobody holds")).String(), true
	})

	var wg sync.WaitGroup

	for _, id := range []string{"one", "two", "three", "four"} {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_ = s.Stock(context.Background(),
				ir.Mount{ID: id, Portable: true, Helper: "./none.wasm"},
				filepath.Join(root, "mounts", id, "scope"))
		}()
	}

	wg.Wait()

	if got := busy.most.Load(); got < 2 {
		t.Errorf("four unrelated caches never overlapped (most %d at once)"+
			"\n  a lock over every cache rather than over one serialises a build"+
			" for nothing", got)
	}
}

// inFlight records how many callers were inside at once.
type inFlight struct {
	now  atomic.Int32
	most atomic.Int32
}

func (f *inFlight) enter() {
	if n := f.now.Add(1); n > f.most.Load() {
		f.most.Store(n)
	}
}

func (f *inFlight) leave() { f.now.Add(-1) }

// blocking is a fleet that takes long enough to overlap with itself.
type blocking struct{ at *inFlight }

func (b *blocking) Node(context.Context, ir.NodeID) ([]byte, error) {
	b.at.enter()
	defer b.at.leave()

	time.Sleep(20 * time.Millisecond)

	return nil, context.DeadlineExceeded
}
