package core_test

import (
	"context"
	"encoding/binary"
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

	// salt makes this executor's layers differ from another's for the same
	// step, which is how a *non-reproducible* step is modelled. Without it a
	// rerun stores exactly the bytes it stored last time and puts back whatever
	// the store lost, so a stale entry heals itself and a test cannot see it
	// fail to.
	salt string
}

func (e storingExec) Run(
	ctx context.Context, n *ir.Node, w core.Worker, base []ir.NodeID, sources [][]ir.NodeID,
) (core.Result, error) {
	res, err := e.inner.Run(ctx, n, w, base, sources)
	if err != nil {
		return res, err
	}

	// Stand in for a captured layer: bytes derived from the step, stored for
	// real, and named by their own digest.
	id, size, err := e.store.Put(strings.NewReader("layer for " + n.ID().String() + e.salt))
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
	cache := newMemCache().heldBy(storeBlobs{st})
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
		deleteErr := st.Delete(e.Layer)
		if deleteErr != nil {
			t.Fatal(deleteErr)
		}
	}

	// **Salted, because a reproducible step heals itself.** Rerunning one
	// stores exactly the bytes it stored last time, which puts back what the
	// store lost and makes the stale entry good again - so the poisoning below
	// is invisible unless the rerun's output actually differs. That is the case
	// this exists for: a step whose rerun yields a new layer leaves the cache
	// naming one nobody has.
	third := storingExec{inner: &sim.Executor{Seed: 5}, store: st, t: t, salt: "rerun"}

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

	// **And the build after that hits again**, which is the half this test used
	// to stop one build short of. Degrading to a miss is only half the
	// contract: the rerun publishes a fresh claim, and if the cache keeps the
	// dead one instead - which is exactly what "an entry already here is left
	// alone" does - the key is unhittable for ever. The step misses, reruns,
	// publishes, and the publish is dropped, build after build.
	//
	// It was invisible because the fake cache overwrote where the real one
	// refuses to. A step whose delta is empty is where it bites hardest: the
	// cheapest thing to rerun, and the last thing an author suspects (E974).
	fourth := storingExec{inner: &sim.Executor{Seed: 5}, store: st, t: t, salt: "rerun"}

	s4 := newSched(cache, storeBlobs{st}, fourth)

	_, err = s4.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	if s4.Stats.Misses != 0 {
		t.Errorf("%d steps missed after a build had already replaced what the store lost;"+
			" the cache is keeping claims nobody can use", s4.Stats.Misses)
	}

	if len(fourth.inner.Log) != 0 {
		t.Errorf("%d steps ran again against a store that now holds their results",
			len(fourth.inner.Log))
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

func (e *failExec) Run(
	_ context.Context, _ *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	e.n++
	if e.n > e.after {
		return core.Result{}, errBoom
	}

	// A distinct layer per call, and distinct is the whole of what it must be.
	// `byte(e.n)` wraps at 256, so a fixture that ran long enough would start
	// handing back a layer it had already produced and the test would pass by
	// agreeing with itself (gosec G115).
	var id ir.NodeID

	binary.BigEndian.PutUint64(id[:8], uint64(e.n))

	return core.Result{Layer: id, Captured: true}, nil
}

var errBoom = errors.New("boom")
