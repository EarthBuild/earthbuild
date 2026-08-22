//go:build darwin

package interp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

const rebuildSrc = `VERSION 0.8

build:
    FROM alpine:3.22
    RUN /bin/busybox true
    RUN /bin/busybox echo second
`

// run builds the source once against a shared cache and layer store, returning
// what each step did.
func run(t *testing.T, root string, cache core.ActionCache) []core.Outcome {
	t.Helper()

	p, err := interp.Build(rebuildSrc, "build")
	if err != nil {
		t.Fatal(err)
	}

	sb := exec.NewApple()
	sb.Store = root
	sb.GuestBinary = guestd(t)

	err = sb.Available()
	if err != nil {
		t.Skipf("apple container backend unavailable: %v", err)
	}

	// The VM outlives Close by design, so a test whose sandbox is named after a
	// temporary directory has to take it away - nothing will ever name that one
	// again. Without this each run left a VM and its 1.3GB volume behind (E526).
	defer func() { _ = sb.Remove() }()

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	e.Platform = "linux/arm64"

	rec := &core.Record{}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "vm", IsInvoker: true}},
		Executor: e,
		Cache:    cache,
		Blobs:    store.LayerStore(root),
		Writer:   "test",
		Record:   rec,
	}

	_, err = s.Run(t.Context(), p.Graph)
	if err != nil {
		t.Fatal(err)
	}

	out := make([]core.Outcome, 0, len(rec.Steps))
	for _, r := range rec.Steps {
		out = append(out, r.Outcome)
	}

	return out
}

// TestRebuildIsAllHits is the claim the whole design rests on: building an
// unchanged Earthfile a second time executes nothing.
//
// Every earlier version of this test used a fake layer store that claimed to
// hold everything. This one asks the filesystem.
func TestRebuildIsAllHits(t *testing.T) {
	t.Parallel()

	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	root := t.TempDir()
	cache := memCache{}

	first := run(t, root, cache)
	for i, o := range first {
		if o != core.OutcomeMiss {
			t.Errorf("first build, step %d: %v, want a miss", i, o)
		}
	}

	second := run(t, root, cache)
	for i, o := range second {
		if o != core.OutcomeL1Hit {
			t.Errorf("second build, step %d: %v, want an L1 hit", i, o)
		}
	}

	// The store's index agrees with the store, after a build that pulled an
	// image, ran steps and captured their deltas.
	//
	// The synthetic version of this check exercises the three ways a layer is
	// filed that a unit test can reach; this one exercises whatever the engine
	// actually does, which is the difference that matters. The index is not
	// load-bearing yet and this is the window in which it can be checked at all
	// - once the store is a disk, only its owner can answer (E542).
	missing, claimed, err := store.Index(root).Disagrees()
	if err != nil {
		t.Fatal(err)
	}

	if len(missing) != 0 {
		t.Errorf("a real build filed layers the store index does not record: %v"+
			"\n  a path that files a layer is not going through Publish, and the"+
			"\n  cost is a machine rebuilding what it already has", missing)
	}

	if len(claimed) != 0 {
		t.Errorf("the store index records layers a real build did not leave: %v"+
			"\n  which is a cache hit against a layer that is not there", claimed)
	}
}

// A cache entry whose layer is gone must miss, not hit.
//
// This is the property that makes the cache unpoisonable in the direction that
// matters: losing a layer - to a GC, a partial copy, a corrupted store - costs
// time and nothing else. An entry trusted without its result present would hand
// the next step a base that does not exist.
func TestAMissingLayerCostsTimeNotCorrectness(t *testing.T) {
	t.Parallel()

	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	root := t.TempDir()
	cache := memCache{}

	run(t, root, cache)

	// Evict the layer produced by the last step, as a GC would.
	layers, err := os.ReadDir(filepath.Join(root, "layers"))
	if err != nil {
		t.Fatal(err)
	}

	if len(layers) < 2 {
		t.Fatalf("expected several layers in the store, found %d", len(layers))
	}

	var evicted string

	for _, l := range layers {
		// Evict a step's output rather than the base image, so the rebuild has
		// something to stand on.
		if l.Name() != layers[0].Name() {
			evicted = l.Name()

			break
		}
	}

	err = os.RemoveAll(filepath.Join(root, "layers", evicted))
	if err != nil {
		t.Fatal(err)
	}

	// The build must still succeed, and must re-execute rather than trusting an
	// entry whose result is gone.
	var misses int

	for _, o := range run(t, root, cache) {
		if o == core.OutcomeMiss {
			misses++
		}
	}

	if misses == 0 {
		t.Error("every step hit despite a missing layer; an entry was trusted without its result")
	}
}

var _ = ir.NodeID{}
