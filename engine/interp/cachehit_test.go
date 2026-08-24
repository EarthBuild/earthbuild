package interp_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Building an unchanged graph a second time runs nothing.
//
// This is the property the engine exists for, and it is not implied by anything
// tested so far: determinism says the same input produces the same key, and
// says nothing about whether that key is written down, found again, or trusted
// when it is. Between the two runs the key has to survive being serialised to
// disk and read back by what is, as far as the cache is concerned, a stranger.
//
// A step that re-runs here is a cache miss nobody would notice - the build
// still produces the right answer, just slowly, which is exactly the failure a
// build tool cannot afford and cannot see.
func TestASecondBuildOfAnUnchangedGraphHitsEveryStep(t *testing.T) {
	t.Parallel()

	// Skipped under -short, which is how the race-instrumented run stays
	// usable: these walk every Earthfile in the repository, and instrumentation
	// multiplies that by about ten. They run in full on every ordinary pass.
	if testing.Short() {
		t.Skip("corpus sweep")
	}

	var graphs, steps int

	for _, f := range corpus(t) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}

		for _, target := range targetsIn(string(src)) {
			p, err := interp.Build(string(src), target, interp.WithContext(filepath.Dir(f)))
			if err != nil {
				continue
			}

			// Some steps are *meant* to run again: a host step is never cached
			// (I7) and a `--no-cache` step was declared not to be a function of
			// its inputs. Counted rather than skipped, so a graph containing one
			// still checks all its other steps.
			want := uncacheable(p)

			dir := t.TempDir()

			ac, err := cache.Open(dir)
			if err != nil {
				t.Fatal(err)
			}

			first := runOnce(t, p, ac)
			if first == 0 {
				continue
			}

			graphs++
			steps += first

			// A second cache, opened over the same directory, so the entries
			// have to have been written down rather than remembered.
			again, err := cache.Open(dir)
			if err != nil {
				t.Fatal(err)
			}

			if ran := runOnce(t, p, again); ran != want {
				t.Errorf("%s [%s]: %d of %d steps ran again on an unchanged graph, want %d "+
					"(the ones that are never cached)", f, target, ran, first, want)
			}
		}
	}

	if graphs == 0 {
		t.Fatal("no graph was built twice, so this checked nothing")
	}

	t.Logf("built %d graphs twice, %d steps cached", graphs, steps)
}

// runOnce schedules the graph and reports how many steps actually executed.
func runOnce(t *testing.T, p *interp.Plan, ac *cache.Cache) int {
	t.Helper()

	e := &simExec{bases: map[ir.NodeID][]ir.NodeID{}}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: e,
		Cache:    ac,
		Blobs:    everyBlob{},
		Writer:   "test",
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), p.Graph)
	if err != nil {
		return 0
	}

	return len(e.order)
}

// uncacheable counts the steps that must run on every build.
//
// The three reasons, and they are the scheduler's three: a host step, because
// nothing bounds what it observed; `--no-cache`, because the author has said it
// is not a function of its inputs; and a step inside a WITH DOCKER block,
// because the daemon outlives the build and every image an earlier one left in
// it is state no key describes.
//
// Kept in step with `schedule.go` by hand, which is a duplication worth naming:
// if the two ever disagree this test either fails for a step that is correctly
// uncached, or - worse - passes while a step that should run again does not.
func uncacheable(p *interp.Plan) int {
	var n int

	for _, node := range p.Graph.Nodes() {
		// A guarded step does not run on a build that succeeds - that is what
		// the guard is for - so it is not one of the steps expected to run
		// again. WITH DOCKER carries one: a teardown for the case where the
		// body fails, which on a green build is skipped.
		if node.OnFailure != nil {
			continue
		}

		if node.Op.Kind == ir.OpHost || node.Op.NoCache || node.Op.Docker {
			n++
		}
	}

	return n
}
