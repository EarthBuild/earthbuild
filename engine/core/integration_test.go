package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/sim"
)

// storeBlobs adapts the real blob store to core.BlobStore, which is the only
// part of it the scheduler is allowed to see. The scheduler asks "is this
// result present"; it never reads bytes, because core touches no file
// descriptor.
type storeBlobs struct{ s *blob.Store }

func (b storeBlobs) Has(id ir.NodeID) bool { return b.s.Has(id) }

// storingExec wraps the simulator so that every step's result is a real blob
// in a real store. Without this the simulator invents digests that name nothing,
// and Lookup rightly refuses them - which is correct behaviour but tests the
// wrong thing.
type storingExec struct {
	inner *sim.Executor
	store *blob.Store
	t     *testing.T
}

func (e storingExec) Run(ctx context.Context, n *ir.Node, w core.Worker, base []ir.NodeID, sources [][]ir.NodeID) (core.Result, error) {
	res, err := e.inner.Run(ctx, n, w, base, sources)
	if err != nil {
		return res, err
	}

	// Stand in for a captured layer: bytes derived from the step, stored for
	// real, and named by their own digest.
	id, size, err := e.store.Put(strings.NewReader("layer for " + n.ID().String()))
	if err != nil {
		e.t.Fatal(err)
	}

	res.Layer, res.Bytes = id, size

	return res, nil
}

// TestLookupVerifiesAgainstRealStore joins S1 to S2: the cache's claims are
// checked against blobs that actually exist on disk, rather than against a fake
// that agrees with everything.
//
// It is the first point where a lie in 𝔄 is caught by 𝔅 rather than by a test
// double, which is the arrangement green paper §5.2 relies on: the action cache
// is a claim, the blob store is self-verifying, and the second is what bounds
// the damage the first can do.
func TestLookupVerifiesAgainstRealStore(t *testing.T) {
	t.Parallel()

	st, err := blob.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}
	g := &ir.Graph{Root: chain(img, "a", "b")}

	// A first build, storing every result for real.
	cache := newMemCache()
	first := storingExec{inner: &sim.Executor{Seed: 5}, store: st, t: t}

	_, err = newSched(cache, storeBlobs{st}, first).Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	// A rebuild: every entry names a blob that is genuinely present, so every
	// step hits and nothing executes.
	second := storingExec{inner: &sim.Executor{Seed: 5}, store: st, t: t}

	s := newSched(cache, storeBlobs{st}, second)
	_, err = s.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	if s.Stats.Misses != 0 {
		t.Errorf("entries backed by real blobs missed %d times", s.Stats.Misses)
	}

	if len(second.inner.Log) != 0 {
		t.Errorf("rebuild executed %d steps against a warm real store", len(second.inner.Log))
	}

	// Now delete the blobs. The cache still claims results; the store no longer
	// has them. Every claim must degrade to a miss, and the build must proceed
	// by executing rather than by failing.
	for _, e := range cache.all() {
		err := st.Delete(e.Layer)
		if err != nil {
			t.Fatal(err)
		}
	}

	third := storingExec{inner: &sim.Executor{Seed: 5}, store: st, t: t}

	s3 := newSched(cache, storeBlobs{st}, third)
	_, err = s3.Run(context.Background(), g)
	if err != nil {
		t.Fatalf("dangling entries produced an error; they must degrade to a miss: %v", err)
	}

	if s3.Stats.Hits != 0 {
		t.Errorf("%d entries were trusted after their blobs were deleted", s3.Stats.Hits)
	}

	if len(third.inner.Log) == 0 {
		t.Error("nothing executed, so the dangling entries were used after all")
	}
}

// TestSchedulerReleasesEveryHandle: a leaked handle is a leaked mount on a real
// materialiser, and mount tables are finite. The fake counts outstanding
// handles so the leak is caught here rather than when a machine runs out.
//
// The failing path matters as much as the happy one, so the second half forces
// a step to fail and asserts the handle is still released.
func TestSchedulerReleasesEveryHandle(t *testing.T) {
	t.Parallel()

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}
	g := &ir.Graph{Root: chain(img, "a", "b", "c")}

	m := &sim.Materialiser{}

	s := newSched(newMemCache(), allBlobs{}, &sim.Executor{Seed: 9})
	s.Materialiser = m

	_, err := s.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	if n := m.Outstanding(); n != 0 {
		t.Errorf("%d handles outstanding after a clean build", n)
	}

	// Now a build whose executor fails part way through.
	m2 := &sim.Materialiser{}
	failing := &failExec{after: 2}

	s2 := newSched(newMemCache(), allBlobs{}, failing)
	s2.Materialiser = m2

	_, err = s2.Run(context.Background(), g)
	if err == nil {
		t.Fatal("expected the build to fail")
	}

	if n := m2.Outstanding(); n != 0 {
		t.Errorf("%d handles outstanding after a failed build", n)
	}
}

// failExec fails once it has run a given number of steps.
type failExec struct {
	after int
	n     int
}

func (e *failExec) Run(_ context.Context, _ *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID) (core.Result, error) {
	e.n++
	if e.n > e.after {
		return core.Result{}, errBoom
	}

	return core.Result{Layer: ir.NodeID{byte(e.n)}, Captured: true}, nil
}

var errBoom = errors.New("boom")
