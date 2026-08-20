package core_test

import (
	"context"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// recordingExec keeps the base stack it was handed for each step.
type recordingExec struct {
	// The scheduler runs steps concurrently, so a double that records what it
	// was handed is written from several goroutines. Unguarded, this was an
	// intermittent `race (short)` failure that went unreproduced for days -
	// it needs two steps ready at the same moment, which the ordering usually
	// prevents.
	mu    sync.Mutex
	bases map[string][]ir.NodeID
	// fixed makes every step produce the same layer, as no-op steps do.
	fixed *ir.NodeID
}

func (e *recordingExec) Run(_ context.Context, n *ir.Node, _ core.Worker, base []ir.NodeID, _ [][]ir.NodeID) (core.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.bases == nil {
		e.bases = map[string][]ir.NodeID{}
	}

	e.bases[n.Meta.Source] = append([]ir.NodeID(nil), base...)

	layer := n.ID()
	if e.fixed != nil {
		layer = *e.fixed
	}

	return core.Result{Layer: layer, Captured: true}, nil
}

// A layer must appear at most once in a base stack.
//
// overlayfs refuses a repeated lowerdir with ELOOP - "too many levels of
// symbolic links" - which names nothing about the cause and appears only on a
// real mount. The simulator accepts duplicates happily, so this went unnoticed
// until an actual overlay refused it.
func TestBaseStacksHaveNoDuplicates(t *testing.T) {
	t.Parallel()

	img := &ir.Node{
		Op:   ir.Op{Kind: ir.OpImage, Args: []string{testImage}},
		Meta: ir.Meta{Source: at(1)},
	}
	a := &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{"a"}}, Inputs: []*ir.Node{img},
		Meta: ir.Meta{Source: at(2)},
	}
	b := &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{"b"}}, Inputs: []*ir.Node{a},
		Meta: ir.Meta{Source: at(3)},
	}

	e := &recordingExec{}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: e,
		Blobs:    allBlobs{},
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: b})
	if err != nil {
		t.Fatal(err)
	}

	for src, base := range e.bases {
		seen := map[ir.NodeID]bool{}

		for _, id := range base {
			if seen[id] {
				t.Errorf("%s: layer %s appears twice in a base stack of %d", src, id, len(base))
			}

			seen[id] = true
		}
	}

	// And the stack must still grow along the chain: the third step sits on the
	// image plus the two steps before it.
	if got := len(e.bases[at(3)]); got != 2 {
		t.Errorf("the last step's base has %d layers, want 2 (image + one step)", got)
	}
}

// A caller that supplies a Record must get it filled in.
//
// Run used to replace s.Record unconditionally, so every caller holding a
// pointer to the record it passed in read an empty one - and a build record that
// is silently empty looks exactly like a build that did nothing.
func TestCallerSuppliedRecordIsPopulated(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"x"}}, Meta: ir.Meta{Source: at(1)}}

	rec := &core.Record{}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: &recordingExec{},
		Blobs:    allBlobs{},
		Record:   rec,
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: n})
	if err != nil {
		t.Fatal(err)
	}

	if len(rec.Steps) != 1 {
		t.Errorf("the caller's record holds %d steps, want 1", len(rec.Steps))
	}
}

// A local context is a source to copy *from*, not a layer to stand *on*.
//
// Stacking it would merge the host's files into the image at the paths they have
// on the host, so `COPY src/main.go /app/` would silently also produce
// /src/main.go. The destination is what COPY is for; the source location is an
// accident of the developer's directory layout.
//
// The context arrives in Sources rather than Inputs, which is what now keeps it
// out of the stack. That is a structural guarantee rather than a check on an
// input's kind - a check only worked while a context was the sole thing a step
// could read without standing on.
func TestLocalContextsAreNotStacked(t *testing.T) {
	t.Parallel()

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}, Meta: ir.Meta{Source: at(1)}}
	ctx := &ir.Node{Op: ir.Op{Kind: ir.OpLocal, Args: []string{testDir}}, Meta: ir.Meta{Source: at(2)}}
	cp := &ir.Node{
		Op:      ir.Op{Kind: ir.OpFile, Args: []string{testDir, "/app/"}},
		Inputs:  []*ir.Node{img},
		Sources: []*ir.Node{ctx},
		Meta:    ir.Meta{Source: at(2)},
	}
	run := &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{"build"}}, Inputs: []*ir.Node{cp},
		Meta: ir.Meta{Source: at(3)},
	}

	e := &recordingExec{}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: e,
		Blobs:    allBlobs{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: run})
	if err != nil {
		t.Fatal(err)
	}

	// COPY stands on the image alone.
	if got := len(e.bases[at(2)]); got != 1 {
		t.Errorf("COPY's base has %d layers, want 1 (the image, not the context)", got)
	}

	// And the step after it stands on the image plus the copy's own output.
	if got := len(e.bases[at(3)]); got != 2 {
		t.Errorf("the step after COPY has a base of %d layers, want 2", got)
	}

	for _, id := range e.bases[at(2)] {
		if id == ctx.ID() {
			t.Error("the local context was stacked into COPY's base")
		}
	}
}

// Two steps that produce identical output produce the same layer - that is the
// deduplication property working - and the stack must not then name it twice.
//
// It is the common case, not a corner: any two steps that write nothing both
// produce the empty layer. overlayfs refuses a repeated lowerdir with ELOOP, so
// a build with two `test` commands in a row would fail on a real mount while
// passing every simulated one.
//
// Dropping the earlier occurrence is safe precisely because the layers are
// identical: they are the same content, so which one is kept cannot matter.
func TestRepeatedLayersAreCollapsed(t *testing.T) {
	t.Parallel()

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}, Meta: ir.Meta{Source: at(1)}}

	// An executor whose steps all produce the same layer, as no-op steps do.
	same := ir.NodeID{9}

	prev := img
	for i, src := range []string{at(2), at(3), at(4)} {
		prev = &ir.Node{
			Op:     ir.Op{Kind: ir.OpExec, Args: []string{"noop", string(rune('a' + i))}},
			Inputs: []*ir.Node{prev},
			Meta:   ir.Meta{Source: src},
		}
	}

	e := &recordingExec{fixed: &same}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: e,
		Blobs:    allBlobs{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: prev})
	if err != nil {
		t.Fatal(err)
	}

	for src, base := range e.bases {
		seen := map[ir.NodeID]bool{}

		for _, id := range base {
			if seen[id] {
				t.Errorf("%s: layer %s appears twice; overlayfs refuses that with ELOOP", src, id)
			}

			seen[id] = true
		}
	}
}

// A --no-cache step is neither served from the cache nor published to it.
//
// The author has said the step is not a function of its inputs - it fetches
// something, or reads the clock. Serving it from cache would hand back a stale
// result and report success, and publishing it would inflict that on the next
// build too.
func TestANoCacheStepIsNotCached(t *testing.T) {
	t.Parallel()

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}, Meta: ir.Meta{Source: at(1)}}
	fetch := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"fetch"}, NoCache: true},
		Inputs: []*ir.Node{img},
		Meta:   ir.Meta{Source: at(2)},
	}

	e := &recordingExec{}
	rec := &core.Record{}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: e,
		Blobs:    allBlobs{},
		Record:   rec,
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: fetch})
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range rec.Steps {
		if r.Meta.Source != at(2) {
			continue
		}

		// A miss is what "it executed" is called here: the step ran because
		// nothing was looked up for it.
		if r.Outcome == core.OutcomeL1Hit || r.Outcome == core.OutcomeL2Hit {
			t.Errorf("a --no-cache step was served from the cache (%v)", r.Outcome)
		}
	}
}
