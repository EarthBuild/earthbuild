package fleet_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step that reads what nobody predicted still runs.
//
// **The safety property the lazy path was resting on and never had.** A
// prediction is advice (I5): a worker that believes a wrong one has fetched the
// wrong tenth of a base, and the step then asks for a file that is not there.
// Since E326 lazy is the configuration that wins, so "the prediction was wrong"
// is a case every build will meet - a step that reads a new header, a compiler
// that consults a file it did not last time.
//
// The engine already has the shape of the answer: `core.ErrInputMissing` says
// "an input could not be obtained" and is *not* a failure. A worker answers it
// by fetching the whole base and running the step again. One retry, because the
// second attempt stands on everything there is - a third could only repeat the
// second.
func TestAStepThatReadsWhatNobodyPredictedStillRuns(t *testing.T) {
	t.Parallel()

	held := layerStore(t)
	id := seedLayer(t, held, 3)

	frags := &fleet.Fragments{Root: t.TempDir()}
	into := layerStore(t)

	e := &faultingExec{missing: id}

	run := fleet.Runner(e, core.Worker{ID: "w"},
		fleet.WithFragments(frags, localFragments{from: held}),
		fleet.WithBlobs(into, &fleet.LayerSource{Held: held, Label: "origin"}))

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{id},
		Hints:   fleet.Hints{ReadsPredicted: []string{"usr/lib/lib0.so"}},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.Refused != "" {
		t.Fatalf("a wrong prediction refused the step: %s"+
			"\n  a hint that can fail a build is not advice (I5, E327)",
			reply.Refused)
	}

	if e.runs != 2 {
		t.Errorf("the step ran %d time(s), want 2 - once on the fragment and"+
			" once on the whole base", e.runs)
	}

	// The retry must not be primed from the prediction that has just been shown
	// to be wrong about this step: priming lazily again would fetch the same
	// wrong tenth and fault on the same file.
	if len(e.predicted) != 2 || e.predicted[0] == nil || e.predicted[1] != nil {
		t.Errorf("the step was told to read %v, then %v; the second attempt"+
			" must stand on the whole base (E327)", e.predicted[0],
			e.predicted[1])
	}

	if !into.Has(id) {
		t.Error("the whole base was never fetched, so the retry stood on the" +
			" same missing file as the first attempt")
	}
}

// faultingExec asks for a file it was not given, once.
type faultingExec struct {
	missing ir.NodeID
	runs    int
	always  bool
	// path is the file this executor asks for and was not given.
	path string
	// predicted is what each attempt was told the step would read.
	predicted [][]string
}

func (f *faultingExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	f.runs++
	f.predicted = append(f.predicted, n.Meta.ReadsPredicted)

	if f.always || f.runs == 1 {
		return core.Result{}, core.MissingInputError{
			Layer: f.missing,
			Path:  f.path,
			Where: "read and not predicted",
		}
	}

	return core.Result{Layer: ir.NodeID{9}}, nil
}

// The retry is one, and then the worker refuses.
//
// A second attempt stands on everything there is, so a third could only repeat
// it - and a worker looping on a step that keeps asking for what it cannot get
// is a build that never finishes rather than one that fails, which is worse.
//
// The store **has** the base here, so provisioning succeeds and the retry path
// is actually reached: the first version of this test used a base nobody had,
// refused during provisioning, and never ran the executor at all. *Failure
// class: a test written for a case that does not exist.*
func TestTheRetryIsOneAndThenTheWorkerRefuses(t *testing.T) {
	t.Parallel()

	held := layerStore(t)
	id := seedLayer(t, held, 2)

	e := &faultingExec{missing: id, always: true}

	run := fleet.Runner(e, core.Worker{ID: "w"}, fleet.WithBlobs(held))

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{id},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.Refused == "" {
		t.Error("a worker whose step kept asking for what it cannot get did" +
			" not refuse")
	}

	if e.runs != 2 {
		t.Errorf("the step ran %d time(s), want 2 - the attempt and one retry",
			e.runs)
	}
}

// A step that reads one unpredicted file fetches one unpredicted file.
//
// **A wrong prediction cost a worker the whole base.** Measured at four workers
// with one step in two reading outside its prediction: 63.6 MiB moved and 7.059s
// against 1.1 MiB and 1.071s - the lazy configuration degrading, in one hop, to
// the whole-layer one that is 2.8x slower than a single machine (E326, E328).
//
// And it costs that **once per worker**, not once per step, because a worker
// keeps its store: one bad hint anywhere and that machine has paid for
// everything. Which is why 1-in-2 and every-step measure the same.
//
// The executor knows which file it wanted. Naming it turns a wrong prediction
// into the cost of the file that was mispredicted, which is what a fault-in is
// for, and leaves the whole-base fetch as the answer to a hint that is wrong
// over and over rather than to one that is wrong once.
func TestAnUnpredictedReadFetchesTheFileNotTheLayer(t *testing.T) {
	t.Parallel()

	held := layerStore(t)
	id := seedLayer(t, held, 3)

	frags := &fleet.Fragments{Root: t.TempDir()}
	into := layerStore(t)

	e := &faultingExec{missing: id, path: "usr/lib/lib2.so"}

	run := fleet.Runner(e, core.Worker{ID: "w"},
		fleet.WithFragments(frags, localFragments{from: held}),
		fleet.WithBlobs(into, &fleet.LayerSource{Held: held, Label: "origin"}))

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{id},
		Hints:   fleet.Hints{ReadsPredicted: []string{"usr/lib/lib0.so"}},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.Refused != "" {
		t.Fatalf("refused: %s", reply.Refused)
	}

	// Under the *combined* name: a fragment is filed by what it contains, and
	// the faulted path is added to the prediction rather than replacing it.
	if !frags.Has(id, []string{"usr/lib/lib0.so", "usr/lib/lib2.so"}) {
		t.Error("the file the step actually read was never fetched")
	}

	if into.Has(id) {
		t.Error("a step that read one unpredicted file pulled the whole base" +
			"\n  one wrong hint should cost one file, not the layer (E328)")
	}

	// The retry still knows what it is missing, so it is told about the file
	// that was faulted in rather than about nothing.
	if len(e.predicted) != 2 || len(e.predicted[1]) == 0 {
		t.Errorf("the retry was told to read %v, want the faulted path",
			e.predicted)
	}
}
