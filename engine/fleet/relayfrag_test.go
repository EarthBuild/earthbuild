package fleet_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A worker serves on the part of a base it holds.
//
// **Otherwise lazy transfer is a star.** Fragments arrive from whoever has the
// whole layer - the driver - and a worker that has just fetched exactly the
// bytes the next machine needs cannot pass them on. That is E260 again, on the
// path that since E323 is the one that wins: adding machines adds queueing at
// the driver rather than throughput.
//
// The relay packs from its own disk, ownership and all. That is sound only
// because a fragment's seal excludes ownership by construction (E324) - the
// receiver could not reproduce it either - and the test runs with the ownership
// seam swapped so the relay's tree genuinely differs from the origin's.
func TestAWorkerServesOnThePartOfABaseItHolds(t *testing.T) {
	// Not parallel: it swaps engine/layer's ownership seam, and that helper now
	// refuses a parallel test outright rather than letting it corrupt one
	// somewhere else (E324). It caught this test on the first run.
	origin := layerStore(t)
	id := seedLayer(t, origin, 3)

	want := []string{"usr/lib/lib1.so"}

	manifest, packed, err := origin.Fragment(id, want)
	if err != nil {
		t.Fatalf("%v", err)
	}

	// The relaying worker, which cannot chown what it unpacks.
	layer.ObservedOwnerForTest(t, func(uid, gid uint32) (uint32, uint32) {
		return uid + 1, gid + 1
	})

	relay := &fleet.Fragments{Root: t.TempDir()}

	err = relay.PutVerified(id, want, manifest, bytes.NewReader(packed))
	if err != nil {
		t.Fatalf("%v", err)
	}

	// Now somebody else asks the relay for the same paths.
	onward, body, err := relay.Fragment(context.Background(), id, want, true)
	if err != nil {
		t.Fatalf("a worker could not serve on what it holds: %v", err)
	}

	if layer.ManifestID(onward) != layer.ManifestID(manifest) {
		t.Error("the relayed proof is not the layer's own")
	}

	// And it must satisfy the next machine, which checks it as strictly as the
	// relay did (E324).
	next := &fleet.Fragments{Root: t.TempDir()}

	if err = next.PutVerified(id, want, onward, bytes.NewReader(body)); err != nil {
		t.Errorf("a relayed fragment was refused downstream: %v\n  a worker"+
			" that cannot pass on what it just fetched makes a fleet a star"+
			" (E325)", err)
	}
}

// A worker does not claim a part of a base it does not have.
//
// The other half: a relay that answered for paths it never fetched would send
// an empty fragment that verifies - it contains nothing that contradicts the
// manifest - and the asking machine would fault on every file it expected.
func TestAWorkerDoesNotServeAPartItLacks(t *testing.T) {
	t.Parallel()

	origin := layerStore(t)
	id := seedLayer(t, origin, 3)

	manifest, packed, err := origin.Fragment(id, []string{"usr/lib/lib1.so"})
	if err != nil {
		t.Fatalf("%v", err)
	}

	relay := &fleet.Fragments{Root: t.TempDir()}

	err = relay.PutVerified(id, []string{"usr/lib/lib1.so"}, manifest,
		bytes.NewReader(packed))
	if err != nil {
		t.Fatalf("%v", err)
	}

	_, _, err = relay.Fragment(context.Background(), id,
		[]string{"usr/lib/lib2.so"}, true)
	if err == nil {
		t.Error("a worker offered a part of a layer it has never seen" +
			"\n  an empty fragment verifies against any manifest (E325)")
	}
}

// A store holding only part of a layer is still asked for that part.
//
// **`Has` answers about the whole layer**, and the blob server used it to decide
// whether to try the fragment path at all - so a worker that holds exactly the
// bytes the next machine wants, and nothing else, is never asked for them. The
// gate and the question were different things wearing the same name.
//
// `Parts` is what a worker's blob endpoint is given: whole layers where it has
// them, parts where it has only those.
func TestAWorkerServesPartsOfLayersItDoesNotWhollyHold(t *testing.T) {
	// Not parallel: swaps engine/layer's ownership seam.
	origin := layerStore(t)
	id := seedLayer(t, origin, 3)

	want := []string{"usr/lib/lib1.so"}

	manifest, packed, err := origin.Fragment(id, want)
	if err != nil {
		t.Fatalf("%v", err)
	}

	layer.ObservedOwnerForTest(t, func(uid, gid uint32) (uint32, uint32) {
		return uid + 1, gid + 1
	})

	frags := &fleet.Fragments{Root: t.TempDir()}

	if err = frags.PutVerified(id, want, manifest, bytes.NewReader(packed)); err != nil {
		t.Fatalf("%v", err)
	}

	// This worker has no whole layers at all.
	held := &fleet.Parts{Whole: layerStore(t), Some: frags}

	if held.Has(id) {
		t.Error("a worker holding one file of a layer claims the whole of it")
	}

	onward, body, err := held.Fragment(id, want)
	if err != nil {
		t.Fatalf("a worker was not asked for the part it holds: %v", err)
	}

	next := &fleet.Fragments{Root: t.TempDir()}

	if err = next.PutVerified(id, want, onward, bytes.NewReader(body)); err != nil {
		t.Errorf("a relayed fragment was refused downstream: %v", err)
	}
}

// A worker that ran a step is named as holding what that step stood on.
//
// **The last hop of the mesh.** `Parts` lets a worker serve what it fetched, and
// nothing told anybody it had it: holders are recorded for layers a worker
// *produced*, and a base is produced by nobody in this build. So every worker
// went on fetching every base from the driver, whose uplink is then the fleet's
// bandwidth - E260, arrived at from the third direction.
//
// The driver knows it without being told: it sent the assignment, and a worker
// that answered rather than refusing had the inputs. Advice like every other
// holder hint - a worker that has only part of a base answers "absent" for the
// whole and the asker falls through to the driver (I6).
func TestAWorkerIsNamedAsHoldingWhatItRanOn(t *testing.T) {
	t.Parallel()

	base := ir.NodeID{7}

	fleet2 := &recordingTransport{repl: fleet.Reply{
		Version: fleet.Version, Layer: ir.NodeID{8}, HeldAt: "w@host:9",
	}}

	d := &fleet.Delegating{Local: refusing{}, Fleet: fleet2}

	_, err := d.Run(t.Context(), execNode(), core.Worker{ID: "w"},
		[]ir.NodeID{base}, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	// A second step on the same base must be told where it already is.
	_, err = d.Run(t.Context(), execNode(), core.Worker{ID: "w"},
		[]ir.NodeID{base}, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if got := fleet2.last().Hints.Holders; len(got) == 0 || got[0] != "w@host:9" {
		t.Errorf("the second step on a base was told its holders are %v"+
			"\n  a worker that just fetched a base is the nearest copy of it,"+
			" and nobody knew (E325)", got)
	}
}

// refusing has no local executor's work to do.
type refusing struct{}

func (refusing) Run(
	context.Context, *ir.Node, core.Worker, []ir.NodeID, [][]ir.NodeID,
) (core.Result, error) {
	return core.Result{}, errNoLocal
}

var errNoLocal = errors.New("nothing runs here")

// execNode is a step. Named around `os/exec`, which a linux-only test in this
// package imports - a collision that only appears on that platform.
func execNode() *ir.Node {
	return &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"make"}}}
}

// recordingTransport keeps the last assignment it was given.
type recordingTransport struct {
	mu   sync.Mutex
	seen fleet.Assignment
	repl fleet.Reply
}

func (r *recordingTransport) Assign(
	_ context.Context, a fleet.Assignment,
) (fleet.Reply, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.seen = a

	return r.repl, nil
}

func (r *recordingTransport) Workers() int { return 1 }

func (r *recordingTransport) last() fleet.Assignment {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.seen
}
