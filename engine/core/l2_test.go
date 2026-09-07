package core_test

import (
	"context"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/sim"
)

// memProfiles is the in-memory profile store.
type memProfiles map[core.Key]core.Observation

func (m memProfiles) Get(k core.Key) (core.Observation, bool) { o, ok := m[k]; return o, ok }
func (m memProfiles) Put(k core.Key, o core.Observation)      { m[k] = o }

// fixedView answers for every stack with one set of files, which is enough to
// drive the consistency check.
type fixedView struct{ base fakeBase }

func (v fixedView) View(context.Context, []ir.NodeID) (core.BaseView, error) {
	return v.base, nil
}

// observingExec reports a scripted observation, standing in for the real
// observer that S5 will build on FUSE or eBPF. The scheduler cannot tell the
// difference, which is the point of the port.
type observingExec struct {
	obs  core.Observation
	mu   sync.Mutex
	runs int
}

func (e *observingExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	e.mu.Lock()
	e.runs++
	e.mu.Unlock()

	// The layer must derive from the node, not from a counter. A counter makes
	// two different builds produce identical digests, so the chain key matches
	// and L1 hits - which quietly destroys any test about L2.
	return core.Result{
		Layer:       n.ID(),
		Observation: e.obs,
		Observed:    true,
		Captured:    true,
	}, nil
}

// TestL2HitsWhenTheBaseChangedButTheReadsDidNot is the claim observed-input
// caching exists to make, and the measurement that says what it is worth.
//
// The base image changes, so the chain key changes and L1 misses. The step read
// nothing that differs, so Κ₂ is unchanged and L2 hits - a rebuild avoided that
// no chain-keyed system could avoid.
func TestL2HitsWhenTheBaseChangedButTheReadsDidNot(t *testing.T) {
	t.Parallel()

	obs := core.Observation{
		Reads: map[string]ir.NodeID{testReadPath: digest(1)},
	}

	view := fixedView{fakeBase{files: map[string]ir.NodeID{testReadPath: digest(1)}}}

	cache := newMemCache()
	profiles := memProfiles{}

	// First build, over base A.
	baseA := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}
	stepNode := func(base *ir.Node) *ir.Node {
		return &ir.Node{
			Op: ir.Op{Kind: ir.OpExec, Args: []string{"cc", testSource}}, Platform: amd64,
			Inputs: []*ir.Node{base},
		}
	}

	e1 := &observingExec{obs: obs}
	s1 := newSched(cache, allBlobs{}, e1)
	s1.Profiles, s1.Views = profiles, view

	_, err := s1.Run(context.Background(), &ir.Graph{Root: stepNode(baseA)})
	if err != nil {
		t.Fatal(err)
	}

	if e1.runs == 0 {
		t.Fatal("first build executed nothing")
	}

	// Second build over a *different* base image. L1 must miss - the chain
	// changed - and L2 must hit, because nothing the step reads differs.
	baseB := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"alpine:3.23"}}, Platform: amd64}

	e2 := &observingExec{obs: obs}
	s2 := newSched(cache, allBlobs{}, e2)
	s2.Profiles, s2.Views = profiles, view

	_, err = s2.Run(context.Background(), &ir.Graph{Root: stepNode(baseB)})
	if err != nil {
		t.Fatal(err)
	}

	if s2.Stats.L2Hits == 0 {
		t.Error("no L2 hit: a base change invalidated a step that could not observe it")
	}
}

// TestL2MissesWhenAPredictionIsStale: if the base changed in a way the step
// *can* observe, the prediction no longer holds and the step must run.
func TestL2MissesWhenAPredictionIsStale(t *testing.T) {
	t.Parallel()

	obs := core.Observation{Reads: map[string]ir.NodeID{testReadPath: digest(1)}}

	cache := newMemCache()
	profiles := memProfiles{}

	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}
	node := &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{"cc", testSource}}, Platform: amd64,
		Inputs: []*ir.Node{base},
	}

	fresh := fixedView{fakeBase{files: map[string]ir.NodeID{testReadPath: digest(1)}}}

	e1 := &observingExec{obs: obs}
	s1 := newSched(cache, allBlobs{}, e1)
	s1.Profiles, s1.Views = profiles, fresh

	_, err := s1.Run(context.Background(), &ir.Graph{Root: node})
	if err != nil {
		t.Fatal(err)
	}

	// The file the step reads has changed.
	stale := fixedView{fakeBase{files: map[string]ir.NodeID{testReadPath: digest(99)}}}

	e2 := &observingExec{obs: obs}
	s2 := newSched(newMemCache(), allBlobs{}, e2)
	s2.Profiles, s2.Views = profiles, stale

	_, err = s2.Run(context.Background(), &ir.Graph{Root: node})
	if err != nil {
		t.Fatal(err)
	}

	if s2.Stats.L2Hits != 0 {
		t.Error("L2 hit despite a changed file the step reads")
	}

	if s2.Stats.L2Stale == 0 {
		t.Error("the stale prediction was not counted")
	}

	if e2.runs == 0 {
		t.Error("the step did not run after its prediction went stale")
	}
}

// TestUnobservedStepsPublishNoObservedKey guards a false-hit trap that is easy
// to fall into.
//
// If a step runs unobserved and we publish a Κ₂ entry anyway, that entry claims
// the step read nothing - and every later step over any base would satisfy that
// claim and falsely hit it. Silence must not be recorded as "read nothing".
func TestUnobservedStepsPublishNoObservedKey(t *testing.T) {
	t.Parallel()

	cache := newMemCache()
	profiles := memProfiles{}

	node := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{testCommand}}, Platform: amd64}

	// sim.Executor reports no observation, because it is not watching.
	s := newSched(cache, allBlobs{}, &sim.Executor{Seed: 1})
	s.Profiles = profiles
	s.Views = fixedView{fakeBase{}}

	_, err := s.Run(context.Background(), &ir.Graph{Root: node})
	if err != nil {
		t.Fatal(err)
	}

	if len(profiles) != 0 {
		t.Error("an unobserved step recorded a profile; silence is not an observation")
	}

	if cache.len() != 1 {
		t.Errorf("cache holds %d entries, want 1 (the chain key only)", cache.len())
	}
}

// TestL2IsOptional: an engine with no profiles or no view is conforming. It is
// slower and never wrong, so the absence must be a quiet skip rather than a
// failure (green paper 4.3).
func TestL2IsOptional(t *testing.T) {
	t.Parallel()

	node := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{testCommand}}, Platform: amd64}

	for _, tc := range []struct {
		name     string
		profiles core.Profiles
		views    core.ViewSource
	}{
		{"neither", nil, nil},
		{"profiles but no view", memProfiles{}, nil},
		{"view but no profiles", nil, fixedView{fakeBase{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newSched(newMemCache(), allBlobs{}, &observingExec{})
			s.Profiles, s.Views = tc.profiles, tc.views

			_, err := s.Run(context.Background(), &ir.Graph{Root: node})
			if err != nil {
				t.Fatalf("absent L2 machinery caused a failure: %v", err)
			}
		})
	}
}
