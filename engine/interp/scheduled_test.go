package interp_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// everyBlob answers that every layer is present.
//
// Declared here rather than borrowed from the darwin-only build test, which is
// where the equivalent lives: this sweep is about scheduling and has no reason
// to be one platform's business. Borrowing it compiled on this machine and
// failed `GOOS=linux go vet`, which is why that check is in the loop.
type everyBlob struct{}

func (everyBlob) Has(ir.NodeID) bool { return true }

// simExec runs nothing and records everything.
//
// A step's result is derived from its identity, so two steps that are the same
// produce the same layer, as real ones do. That is what makes the duplicate and
// ordering checks below meaningful rather than vacuous.
type simExec struct {
	// Locked, because the scheduler really does run steps concurrently - which
	// the first version of this simulator did not allow for, and Go's race
	// detector for maps said so immediately. A fake that is not safe to call
	// the way the real thing is called tests the wrong system.
	mu    sync.Mutex
	order []ir.NodeID
	bases map[ir.NodeID][]ir.NodeID
}

func (e *simExec) Run(_ context.Context, n *ir.Node, _ core.Worker, base []ir.NodeID, _ [][]ir.NodeID) (core.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.order = append(e.order, n.ID())
	e.bases[n.ID()] = append([]ir.NodeID(nil), base...)

	return core.Result{Layer: n.ID(), Captured: true}, nil
}

// Every corpus graph schedules without violating the rules a real backend
// depends on.
//
// The plan-side invariants cannot see any of this: a graph can be perfectly
// well formed and still be scheduled in an order that runs a step before the
// step it stands on, or handed a base stack that overlayfs refuses. Those
// failures appear on a real mount, on someone else's machine, in a build that
// passed here.
//
// Simulated rather than sandboxed, so it covers every graph the corpus can
// produce instead of the handful a VM has time for.
func TestEveryCorpusGraphSchedulesSoundly(t *testing.T) {
	t.Parallel()

	// Skipped under -short, which is how the race-instrumented run stays
	// usable: these walk every Earthfile in the repository, and instrumentation
	// multiplies that by about ten. They run in full on every ordinary pass.
	if testing.Short() {
		t.Skip("corpus sweep")
	}

	var graphs, steps, refused int

	for _, f := range corpus(t) {
		src, err := os.ReadFile(f) //nolint:gosec // a fixture this test wrote
		if err != nil {
			t.Fatal(err)
		}

		for _, target := range targetsIn(string(src)) {
			p, err := interp.Build(string(src), target, interp.WithContext(filepath.Dir(f)))
			if err != nil {
				continue
			}

			graphs++

			e := &simExec{bases: map[ir.NodeID][]ir.NodeID{}}
			rec := &core.Record{}

			s := &core.Scheduler{
				Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
				Executor: e,
				Blobs:    everyBlob{},
				Record:   rec,
			}

			_, err = s.Run(context.Background(), p.Graph)
			if err != nil {
				// A platform this worker cannot run is a legitimate refusal and
				// says so - the rule exists to stop a build silently producing
				// the wrong architecture. It is not a soundness failure, which
				// is what this sweep is about.
				if errors.Is(err, core.ErrNoEligibleWorker) {
					refused++

					continue
				}

				t.Errorf("%s [%s]: the graph would not schedule: %v", f, target, err)

				continue
			}

			where := f + " [" + target + "]"
			steps += len(e.order)

			// Every step ran, and none ran twice. A step quietly skipped is a
			// build that reports success without doing the work.
			done := map[ir.NodeID]int{}
			for _, id := range e.order {
				done[id]++
			}

			for _, n := range p.Graph.Nodes() {
				// A guarded step is *meant* not to run when the step it guards
				// succeeded - that is the whole of what a guard says. WITH
				// DOCKER carries one: a teardown for the case where the body
				// fails. Counting it as a skipped step would make every such
				// block look unsound.
				if n.OnFailure != nil {
					continue
				}

				switch done[n.ID()] {
				case 1:
				case 0:
					t.Errorf("%s: %s (%s) never ran", where, n.Op.Kind, n.Meta.Source)
				default:
					t.Errorf("%s: %s (%s) ran %d times", where, n.Op.Kind, n.Meta.Source, done[n.ID()])
				}
			}

			// A step completed only after everything it stands on completed.
			// Under concurrency this is the strongest ordering statement
			// available, and it is the one that matters: a step that started
			// before its input finished would read a filesystem that does not
			// exist yet.
			seen := map[ir.NodeID]bool{}
			position := map[ir.NodeID]int{}

			for i, id := range e.order {
				position[id] = i
			}

			for _, n := range p.Graph.Nodes() {
				// Likewise: a step that did not run has no position, and
				// comparing one against its inputs' compares zero against a
				// real index.
				if n.OnFailure != nil {
					continue
				}

				for _, in := range n.Inputs {
					if position[in.ID()] > position[n.ID()] {
						t.Errorf("%s: %s ran before %s, which it stands on",
							where, n.Meta.Source, in.Meta.Source)
					}
				}
			}

			_ = seen

			// No layer appears twice in a base stack: overlayfs refuses a
			// repeated lowerdir with ELOOP, which names nothing about the cause
			// and appears only on a real mount.
			for id, base := range e.bases {
				once := map[ir.NodeID]bool{}

				for _, l := range base {
					if once[l] {
						t.Errorf("%s: a base stack repeats a layer (%d deep)", where, len(base))

						break
					}

					once[l] = true
				}

				_ = id
			}
		}
	}

	if graphs == 0 {
		t.Fatal("no graph scheduled, so this checked nothing")
	}

	t.Logf("scheduled %d graphs, %d steps; %d refused for want of a matching worker", graphs, steps, refused)
}

// How much of a real build could be started before the answer is known.
//
// The classification is only worth building a speculator on if it says yes to
// most of a build. This measures that across the corpus, and asserts the one
// property that makes the tiers trustworthy: a graph containing nothing that
// touches the machine must be speculable throughout. If such a graph reported
// "never" anywhere, the classification would be refusing work for a reason
// nobody could name.
func TestMostOfABuildCouldBeSpeculatedOn(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("corpus sweep")
	}

	var freely, retryable, never, graphs int

	for _, f := range corpus(t) {
		src, err := os.ReadFile(f) //nolint:gosec // a fixture this test wrote
		if err != nil {
			t.Fatal(err)
		}

		for _, target := range targetsIn(string(src)) {
			p, err := interp.Build(string(src), target, interp.WithContext(filepath.Dir(f)))
			if err != nil {
				continue
			}

			graphs++

			// grounded: this graph touches something outside itself, so a
			// refusal to speculate is expected rather than a defect. The three
			// ways are the three the classifier knows - a host step, a step the
			// author declared uncacheable, and a step given a docker daemon,
			// which puts images in one that outlives the build.
			var grounded bool

			for _, n := range p.Graph.Nodes() {
				if n.Op.Kind == ir.OpHost || n.Op.NoCache || n.Op.Docker {
					grounded = true
				}
			}

			for _, n := range p.Graph.Nodes() {
				switch core.MaySpeculate(n) {
				case core.SpeculateFreely:
					freely++
				case core.SpeculateRetryable:
					retryable++
				case core.SpeculateNever:
					never++

					if !grounded {
						t.Errorf("%s [%s]: %s (%s) may not be speculated on, though nothing in "+
							"this graph touches the machine", f, target, n.Op.Kind, n.Meta.Source)
					}
				}
			}
		}
	}

	total := freely + retryable + never
	if total == 0 {
		t.Fatal("nothing was classified")
	}

	t.Logf("across %d graphs, %d steps: %d freely (%d%%), %d retryable (%d%%), %d never (%d%%)",
		graphs, total,
		freely, freely*100/total,
		retryable, retryable*100/total,
		never, never*100/total)
}
