package fleet_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// memCache and everyBlob are the smallest scheduler this test can drive.
type memCache struct {
	mu sync.Mutex
	m  map[core.Key]core.Entry
}

func (c *memCache) Get(k core.Key) (core.Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.m[k]

	return e, ok
}

func (c *memCache) Put(k core.Key, e core.Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.m == nil {
		c.m = map[core.Key]core.Entry{}
	}

	c.m[k] = e
}

type everyBlob struct{}

func (everyBlob) Has(ir.NodeID) bool { return true }

// building is an executor that produces a layer determined by the step.
//
// Deterministic on purpose: the claim under test is that **delegation changes
// nothing about what is produced**, and an executor whose output depended on
// where it ran could not tell the difference between that being true and the
// test being weak.
type building struct {
	mu   sync.Mutex
	ran  []string
	name string
}

func (b *building) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.ran = append(b.ran, n.Meta.Source)

	// A digest that is a function of the step and nothing else.
	h := ir.NewHasher()
	h.Str(n.Op.Kind.String())

	for _, a := range n.Op.Args {
		h.Str(a)
	}

	id := h.Sum()

	return core.Result{
		Layer: id, Content: id, Captured: true,
		Observation: core.Observation{
			Reads:    map[string]ir.NodeID{"/base": {1}},
			Listings: map[string]ir.NodeID{},
		},
		Observed: true,
	}, nil
}

func (b *building) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return len(b.ran)
}

// chain is a few steps stacked on a base.
func chain(n int) *ir.Graph {
	node := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"alpine"}}}

	for i := range n {
		node = &ir.Node{
			Op:     ir.Op{Kind: ir.OpExec, Args: []string{"step", fmt.Sprint(i)}},
			Inputs: []*ir.Node{node},
			Meta:   ir.Meta{Source: fmt.Sprintf("Earthfile:%d", i+2)},
		}
	}

	return &ir.Graph{Root: node}
}

// A build through a fleet produces what a build without one produces.
//
// **The claim delegation has to earn.** Everything else in this package is about
// what may cross the wire; this is about the answer being the same on the other
// side. A fleet that produced different layers would be a correctness failure
// wearing a performance feature's clothes, and it would not show up in any of
// the structural tests - they check that a message is well-formed, not that the
// build is right.
//
// The in-process fleet does the work with the same executor, so what is being
// compared is the *path*: `Delegate` -> `Assign` -> `Reply` -> `resultOf` ->
// scheduler, against the scheduler alone.
func TestABuildThroughAFleetProducesWhatALocalBuildProduces(t *testing.T) {
	t.Parallel()

	const steps = 4

	// Local: one worker, which is this machine.
	solo := &building{name: "local"}
	local := &core.Scheduler{
		Workers:  []core.Worker{{ID: "me", IsInvoker: true}},
		Executor: solo,
		Cache:    &memCache{},
		Blobs:    everyBlob{},
		Writer:   "test",
	}

	_, err := local.Run(context.Background(), chain(steps))
	if err != nil {
		t.Fatal(err)
	}

	// Delegated: two workers, one of them not this machine, and the fleet does
	// the work with the same executor.
	remote := &building{name: "remote"}
	here := &building{name: "here"}

	f := &fleet.InProcess{}
	f.AddWorker(func(ctx context.Context, a fleet.Assignment) (fleet.Reply, error) {
		n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: a.Op.Args}}

		r, runErr := remote.Run(ctx, n, core.Worker{ID: "w1"}, a.Base, a.Sources)
		if runErr != nil {
			return fleet.Reply{}, runErr
		}

		return fleet.Reply{
			Version: fleet.Version, Layer: r.Layer, Content: r.Content,
			Observation: fleet.Observation{Reads: r.Observation.Reads},
		}, nil
	})

	fleeted := &core.Scheduler{
		Workers: []core.Worker{
			{ID: "me", IsInvoker: true},
			{ID: "w1"},
		},
		Executor: &fleet.Delegating{Local: here, Fleet: f},
		Cache:    &memCache{},
		Blobs:    everyBlob{},
		Writer:   "test",
	}

	_, err = fleeted.Run(context.Background(), chain(steps))
	if err != nil {
		t.Fatal(err)
	}

	// **Without this the comparison is between two local builds.** A scheduler
	// that placed everything on the invoker would produce identical layers and
	// prove nothing - which is the shape of a green gate over a feature that is
	// not running (E90).
	if remote.count() == 0 {
		t.Fatalf("no step went through the fleet (%d ran here), so this"+
			" compares two local builds", here.count())
	}

	t.Logf("%d steps delegated, %d built here", remote.count(), here.count())

	want, got := local.Record.Steps, fleeted.Record.Steps

	if len(want) != len(got) {
		t.Fatalf("the two builds recorded %d and %d steps", len(want), len(got))
	}

	for i := range want {
		if want[i].Layer != got[i].Layer {
			t.Errorf("step %d produced %v locally and %v through the fleet"+
				"\n  delegation must change nothing about what is produced",
				i, want[i].Layer, got[i].Layer)
		}
	}
}
